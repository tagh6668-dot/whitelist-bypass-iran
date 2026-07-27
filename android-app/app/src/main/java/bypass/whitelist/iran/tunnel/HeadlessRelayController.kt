package bypass.whitelist.iran.tunnel

import android.content.Context
import android.net.ConnectivityManager
import android.util.Log
import bypass.whitelist.iran.util.DnsMode
import bypass.whitelist.iran.util.ParamCallback
import bypass.whitelist.iran.util.Prefs
import bypass.whitelist.iran.util.SettingsManager
import bypass.whitelist.iran.util.SocksAuth
import bypass.whitelist.iran.util.Vpn
import org.json.JSONObject
import java.io.BufferedWriter
import java.io.File
import java.io.OutputStreamWriter

class HeadlessRelayController(
    private val context: Context,
    private val relayMode: String = "bale-headless-joiner",
    private val onLog: ParamCallback<String>,
    private val onStatus: ParamCallback<VpnStatus>,
) {
    private var process: Process? = null
    private var thread: Thread? = null
    private var stdinWriter: BufferedWriter? = null
    private val pendingCommands = mutableListOf<String>()

    @Volatile
    var isRunning = false
        private set

    fun start() {
        stop()
        isRunning = true

        val relayBin = File(context.applicationInfo.nativeLibraryDir, "librelay.so")
        if (!relayBin.exists()) {
            onLog("Relay binary not found")
            onStatus(VpnStatus.CALL_FAILED)
            return
        }

        thread = Thread {
            val socksPort = Prefs.socksPort
            if (!PortGuard.ensurePortFree(socksPort)) {
                onLog("SOCKS5 port $socksPort is busy and could not be freed")
                onStatus(VpnStatus.PORT_BUSY)
                isRunning = false
                return@Thread
            }
            try {
                val args = mutableListOf(
                    relayBin.absolutePath,
                    "--mode", relayMode,
                    "--socks-port", "$socksPort",
                    "--socks-user", SocksAuth.user,
                    "--socks-pass", SocksAuth.pass
                )

                val dnsServers = if (Prefs.dnsMode == DnsMode.CUSTOM) {
                    val custom = listOf(Prefs.dnsPrimary.trim(), Prefs.dnsSecondary.trim()).filter { it.isNotEmpty() }
                    if (custom.isNotEmpty()) custom else listOf(Vpn.DNS_PRIMARY, Vpn.DNS_SECONDARY)
                } else {
                    getSystemDnsServers().ifEmpty { listOf(Vpn.DNS_PRIMARY, Vpn.DNS_SECONDARY) }
                }

                if (dnsServers.isNotEmpty()) {
                    args.add("--system-dns")
                    args.add(dnsServers.joinToString(","))
                    onLog("DNS servers passed (${Prefs.dnsMode}): ${dnsServers.joinToString(",")}")
                }

                if (Prefs.localDnsEnabled) {
                    args.add("--local-dns")
                    if (Prefs.fakeDnsEnabled) {
                        args.add("--fake-dns")
                    }
                    args.add("--remote-dns")
                    args.add(SettingsManager.getRemoteDnsServers().joinToString(","))
                    args.add("--domestic-dns")
                    args.add(SettingsManager.getDomesticDnsServers().joinToString(","))
                    args.add("--local-dns-port")
                    args.add(Prefs.localDnsPort)
                    onLog("Local DNS active (Fake DNS: ${Prefs.fakeDnsEnabled}, Port: ${Prefs.localDnsPort})")
                }

                if (Prefs.routingEnabled) {
                    val routingFile = File(context.filesDir, "routing.json")
                    try {
                        val routingJson = try {
                            val obj = JSONObject(Prefs.routingConfigJson)
                            obj.put("domainStrategy", Prefs.routingDomainStrategy)
                            obj.put("localDnsEnabled", Prefs.localDnsEnabled)
                            obj.put("fakeDnsEnabled", Prefs.fakeDnsEnabled)
                            obj.put("remoteDns", Prefs.remoteDns)
                            obj.put("domesticDns", Prefs.domesticDns)
                            obj.put("localDnsPort", Prefs.localDnsPort)
                            obj.toString(2)
                        } catch (_: Exception) {
                            Prefs.routingConfigJson
                        }

                        routingFile.writeText(routingJson)
                        args.add("--routing-config")
                        args.add(routingFile.absolutePath)
                        onLog("Routing config loaded: ${routingFile.absolutePath}")
                    } catch (e: Exception) {
                        onLog("Failed to write routing config: ${e.message}")
                    }
                }

                val pb = ProcessBuilder(args)
                pb.redirectErrorStream(true)
                val proc = pb.start()
                synchronized(this) {
                    process = proc
                    stdinWriter = BufferedWriter(OutputStreamWriter(proc.outputStream))
                    pendingCommands.forEach { writeStdin(it) }
                    pendingCommands.clear()
                }
                onLog("Headless relay started mode=$relayMode SOCKS5 ${SocksAuth.user}:${SocksAuth.pass}@127.0.0.1:$socksPort")

                proc.inputStream.bufferedReader().forEachLine { line ->
                    when {
                        line.startsWith("RESOLVE:") -> {
                            val hostname = line.removePrefix("RESOLVE:")
                            try {
                                val resolvedIP = resolveHostname(hostname, dnsServers)
                                Log.d("RELAY", "Resolved $hostname -> $resolvedIP")
                                writeStdin(resolvedIP)
                            } catch (e: Exception) {
                                Log.e("RELAY", "DNS resolve failed for $hostname", e)
                                writeStdin("")
                            }
                        }
                        line.startsWith("STATUS:") -> {
                            val status = line.removePrefix("STATUS:")
                            Log.d("RELAY", "status: $status")
                            when {
                                status == "READY" -> onStatus(VpnStatus.STARTING)
                                status == "CONNECTING" -> onStatus(VpnStatus.CONNECTING)
                                status == "TUNNEL_CONNECTED" -> onStatus(VpnStatus.TUNNEL_ACTIVE)
                                status == "TUNNEL_LOST" -> onStatus(VpnStatus.TUNNEL_LOST)
                                status.startsWith("ERROR") -> {
                                    val msg = status.substringAfter("ERROR:", "")
                                    if (msg.isNotEmpty()) onLog("Relay error: $msg")
                                    onStatus(VpnStatus.CALL_FAILED)
                                }
                            }
                        }
                        else -> {
                            Log.d("RELAY", line)
                            onLog(line)
                            if (line.contains("pub PC state: connected")) onStatus(VpnStatus.CALL_CONNECTED)
                        }
                    }
                }
                proc.waitFor()
                Log.d("RELAY", "Headless relay exited: ${proc.exitValue()}")
            } catch (e: Exception) {
                if (isRunning) {
                    Log.e("RELAY", "Headless relay error", e)
                    onLog("Relay error: ${e.message}")
                    onStatus(VpnStatus.CALL_FAILED)
                }
            }
        }.also { it.start() }
    }

    fun sendJoinParams(joinJson: String) {
        writeStdin("JOIN:$joinJson")
    }

    @Synchronized
    fun stop() {
        isRunning = false
        process?.let {
            it.destroy()
            it.waitFor()
        }
        process = null
        stdinWriter = null
        thread?.interrupt()
        thread = null
    }

    @Synchronized
    private fun writeStdin(line: String) {
        if (stdinWriter == null) {
            pendingCommands.add(line)
            return
        }
        try {
            stdinWriter?.write(line)
            stdinWriter?.newLine()
            stdinWriter?.flush()
        } catch (e: Exception) {
            Log.e("RELAY", "writeStdin error: ${e.message}")
        }
    }

    private fun getSystemDnsServers(): List<String> {
        val connectivityManager = context.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return emptyList()
        val network = connectivityManager.activeNetwork ?: return emptyList()
        val linkProperties = connectivityManager.getLinkProperties(network) ?: return emptyList()
        return linkProperties.dnsServers.mapNotNull { it.hostAddress }
    }

    private fun isPoisonedIp(ip: String): Boolean {
        if (ip.isEmpty() || ip == "10.10.34.34" || ip.startsWith("10.10.34.") || ip == "127.0.0.1" || ip == "0.0.0.0") return true
        return false
    }

    private fun resolveHostname(hostname: String, dnsServers: List<String>): String {
        if (hostname.matches(Regex("""^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$"""))) {
            return hostname
        }

        if (Prefs.dnsMode == DnsMode.CUSTOM || dnsServers.isNotEmpty()) {
            for (server in dnsServers) {
                val ip = queryDnsUdp(hostname, server)
                if (!ip.isNullOrEmpty() && !isPoisonedIp(ip)) {
                    return ip
                }
            }
        }

        return try {
            val addrs = java.net.InetAddress.getAllByName(hostname)
            val clean = addrs.firstOrNull { !isPoisonedIp(it.hostAddress ?: "") }
            clean?.hostAddress ?: addrs.first().hostAddress ?: ""
        } catch (e: Exception) {
            ""
        }
    }

    private fun queryDnsUdp(hostname: String, dnsServer: String): String? {
        return try {
            val socket = java.net.DatagramSocket()
            socket.soTimeout = 1500

            val query = buildDnsQueryPacket(hostname)
            val serverAddr = java.net.InetAddress.getByName(dnsServer)
            val packet = java.net.DatagramPacket(query, query.size, serverAddr, 53)
            socket.send(packet)

            val recvBuf = ByteArray(512)
            val recvPacket = java.net.DatagramPacket(recvBuf, recvBuf.size)
            socket.receive(recvPacket)
            socket.close()

            parseDnsResponseIp(recvBuf, recvPacket.length)
        } catch (e: Exception) {
            null
        }
    }

    private fun buildDnsQueryPacket(hostname: String): ByteArray {
        val bos = java.io.ByteArrayOutputStream()
        bos.write(byteArrayOf(0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00))
        for (part in hostname.split(".")) {
            val bytes = part.toByteArray(Charsets.US_ASCII)
            bos.write(bytes.size)
            bos.write(bytes)
        }
        bos.write(0)
        bos.write(byteArrayOf(0x00, 0x01, 0x00, 0x01))
        return bos.toByteArray()
    }

    private fun parseDnsResponseIp(buf: ByteArray, len: Int): String? {
        if (len < 12) return null
        val anCount = ((buf[6].toInt() and 0xFF) shl 8) or (buf[7].toInt() and 0xFF)
        if (anCount == 0) return null

        var pos = 12
        while (pos < len && buf[pos].toInt() != 0) {
            pos += (buf[pos].toInt() and 0xFF) + 1
        }
        pos += 5

        for (i in 0 until anCount) {
            if (pos >= len) break
            if ((buf[pos].toInt() and 0xC0) == 0xC0) {
                pos += 2
            } else {
                while (pos < len && buf[pos].toInt() != 0) {
                    pos += (buf[pos].toInt() and 0xFF) + 1
                }
                pos += 1
            }
            if (pos + 10 > len) break
            val qType = ((buf[pos].toInt() and 0xFF) shl 8) or (buf[pos + 1].toInt() and 0xFF)
            val rdLen = ((buf[pos + 8].toInt() and 0xFF) shl 8) or (buf[pos + 9].toInt() and 0xFF)
            pos += 10

            if (qType == 1 && rdLen == 4 && pos + 4 <= len) {
                return "${buf[pos].toInt() and 0xFF}.${buf[pos + 1].toInt() and 0xFF}.${buf[pos + 2].toInt() and 0xFF}.${buf[pos + 3].toInt() and 0xFF}"
            }
            pos += rdLen
        }
        return null
    }
}
