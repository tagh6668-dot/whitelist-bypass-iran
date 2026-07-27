package bypass.whitelist.iran.tunnel

import android.content.Context
import android.net.ConnectivityManager
import android.util.Log
import bypass.whitelist.iran.util.DnsMode
import bypass.whitelist.iran.util.ParamCallback
import bypass.whitelist.iran.util.Prefs
import bypass.whitelist.iran.util.SocksAuth
import bypass.whitelist.iran.util.Vpn
import java.io.BufferedWriter
import java.io.File
import java.io.OutputStreamWriter
import java.net.Inet4Address
import java.net.InetAddress

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

                if (Prefs.routingEnabled) {
                    val routingFile = File(context.filesDir, "routing.json")
                    try {
                        routingFile.writeText(Prefs.routingConfigJson)
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

        try {
            val all = InetAddress.getAllByName(hostname)
            val address = all.firstOrNull { it is Inet4Address && !isPoisonedIp(it.hostAddress ?: "") }
                ?: all.firstOrNull { !isPoisonedIp(it.hostAddress ?: "") }
            val resolved = address?.hostAddress ?: ""
            if (resolved.isNotEmpty() && !isPoisonedIp(resolved)) {
                return resolved
            }
        } catch (_: Exception) {
        }

        val fallbackServers = listOf("1.1.1.1", "8.8.8.8", "1.0.0.1")
        for (server in fallbackServers) {
            val ip = queryDnsUdp(hostname, server)
            if (!ip.isNullOrEmpty() && !isPoisonedIp(ip)) {
                return ip
            }
        }

        return ""
    }

    private fun queryDnsUdp(hostname: String, dnsServerIp: String, timeoutMs: Int = 2000): String? {
        var socket: java.net.DatagramSocket? = null
        try {
            socket = java.net.DatagramSocket()
            socket.soTimeout = timeoutMs
            val random = java.util.Random()
            val txId = random.nextInt(65535)

            val baos = java.io.ByteArrayOutputStream()
            val dos = java.io.DataOutputStream(baos)

            dos.writeShort(txId)
            dos.writeShort(0x0100)
            dos.writeShort(0x0001)
            dos.writeShort(0x0000)
            dos.writeShort(0x0000)
            dos.writeShort(0x0000)

            for (part in hostname.split(".")) {
                val bytes = part.toByteArray(Charsets.UTF_8)
                dos.writeByte(bytes.size)
                dos.write(bytes)
            }
            dos.writeByte(0)
            dos.writeShort(0x0001)
            dos.writeShort(0x0001)

            val queryData = baos.toByteArray()
            val serverAddr = InetAddress.getByName(dnsServerIp)
            val sendPacket = java.net.DatagramPacket(queryData, queryData.size, serverAddr, 53)
            socket.send(sendPacket)

            val recvBuf = ByteArray(512)
            val recvPacket = java.net.DatagramPacket(recvBuf, recvBuf.size)
            socket.receive(recvPacket)

            val resp = recvPacket.data
            if (resp.size < 12) return null

            val dis = java.io.DataInputStream(java.io.ByteArrayInputStream(resp))
            val respId = dis.readUnsignedShort()
            if (respId != txId) return null

            val flags = dis.readUnsignedShort()
            if ((flags and 0x000F) != 0) return null

            val qdCount = dis.readUnsignedShort()
            val anCount = dis.readUnsignedShort()
            dis.readUnsignedShort()
            dis.readUnsignedShort()

            for (i in 0 until qdCount) {
                while (true) {
                    val len = dis.readUnsignedByte()
                    if (len == 0) break
                    if (len >= 192) {
                        dis.readByte()
                        break
                    }
                    dis.skipBytes(len)
                }
                dis.readUnsignedShort()
                dis.readUnsignedShort()
            }

            for (i in 0 until anCount) {
                fun skipName() {
                    val b = dis.readUnsignedByte()
                    if (b >= 192) {
                        dis.readByte()
                    } else if (b > 0) {
                        dis.skipBytes(b)
                        skipName()
                    }
                }
                skipName()

                val type = dis.readUnsignedShort()
                dis.readUnsignedShort()
                dis.readInt()
                val rdLength = dis.readUnsignedShort()

                if (type == 1 && rdLength == 4) {
                    val ipBytes = ByteArray(4)
                    dis.readFully(ipBytes)
                    val ipStr = InetAddress.getByAddress(ipBytes).hostAddress
                    if (ipStr != null && !isPoisonedIp(ipStr)) {
                        return ipStr
                    }
                } else {
                    dis.skipBytes(rdLength)
                }
            }
        } catch (e: Exception) {
            Log.w("RELAY", "UDP DNS query to $dnsServerIp failed for $hostname: ${e.message}")
        } finally {
            try { socket?.close() } catch (_: Exception) {}
        }
        return null
    }
}
