package bypass.whitelist.iran.util

import android.content.Context
import android.text.TextUtils
import bypass.whitelist.iran.dto.RulesetItem
import bypass.whitelist.iran.enums.RoutingType
import org.json.JSONArray
import org.json.JSONObject

object SettingsManager {

    const val DEFAULT_REMOTE_DNS = "https://1.1.1.1/dns-query"
    const val DEFAULT_DOMESTIC_DNS = "223.5.5.5,119.29.29.29"

    fun getRemoteDnsServers(): List<String> {
        val remoteDns = Prefs.remoteDns.ifBlank { DEFAULT_REMOTE_DNS }
        val servers = remoteDns.split(",").map { it.trim() }.filter { it.isNotEmpty() }
        return if (servers.isNotEmpty()) servers else listOf(DEFAULT_REMOTE_DNS)
    }

    fun getDomesticDnsServers(): List<String> {
        val domesticDns = Prefs.domesticDns.ifBlank { DEFAULT_DOMESTIC_DNS }
        val servers = domesticDns.split(",").map { it.trim() }.filter { it.isNotEmpty() }
        return if (servers.isNotEmpty()) servers else listOf("223.5.5.5", "119.29.29.29")
    }

    fun getVpnDnsServers(): List<String> {
        val vpnDns = Prefs.vpnDns.trim()
        if (vpnDns.isNotEmpty()) {
            val servers = vpnDns.split(",").map { it.trim() }.filter { it.isNotEmpty() }
            if (servers.isNotEmpty()) return servers
        }
        val primary = Prefs.dnsPrimary.trim().ifEmpty { Vpn.DNS_PRIMARY }
        val secondary = Prefs.dnsSecondary.trim().ifEmpty { Vpn.DNS_SECONDARY }
        return listOf(primary, secondary)
    }

    fun getPresetRoutingRulesets(context: Context, index: Int = 3): List<RulesetItem>? {
        val fileName = RoutingType.fromIndex(index).fileName
        return try {
            val content = context.assets.open(fileName).bufferedReader().use { it.readText() }
            parseRulesets(content)
        } catch (e: Exception) {
            null
        }
    }

    fun parseRulesets(content: String): List<RulesetItem> {
        val list = mutableListOf<RulesetItem>()
        if (content.isBlank()) return list
        try {
            val array = JSONArray(content)
            for (i in 0 until array.length()) {
                val obj = array.getJSONObject(i)
                val item = RulesetItem(
                    id = obj.optString("id", i.toString()),
                    remarks = obj.optString("remarks", ""),
                    outboundTag = obj.optString("outboundTag", "direct"),
                    port = obj.optString("port", null),
                    network = obj.optString("network", null),
                    enabled = obj.optBoolean("enabled", true),
                    locked = obj.optBoolean("locked", false)
                )
                if (obj.has("ip")) {
                    val ipArr = obj.getJSONArray("ip")
                    val ips = mutableListOf<String>()
                    for (j in 0 until ipArr.length()) ips.add(ipArr.getString(j))
                    item.ip = ips
                }
                if (obj.has("domain")) {
                    val domArr = obj.getJSONArray("domain")
                    val doms = mutableListOf<String>()
                    for (j in 0 until domArr.length()) doms.add(domArr.getString(j))
                    item.domain = doms
                }
                if (obj.has("process")) {
                    val procArr = obj.getJSONArray("process")
                    val procs = mutableListOf<String>()
                    for (j in 0 until procArr.length()) procs.add(procArr.getString(j))
                    item.process = procs
                }
                list.add(item)
            }
        } catch (_: Exception) {
        }
        return list
    }

    fun rulesetsToJson(rulesets: List<RulesetItem>): String {
        val array = JSONArray()
        for (item in rulesets) {
            val obj = JSONObject()
            if (item.id.isNotEmpty()) obj.put("id", item.id)
            if (!item.remarks.isNullEmpty()) obj.put("remarks", item.remarks)
            obj.put("outboundTag", item.outboundTag)
            if (!item.port.isNullEmpty()) obj.put("port", item.port)
            if (!item.network.isNullEmpty()) obj.put("network", item.network)
            obj.put("enabled", item.enabled)
            if (item.locked == true) obj.put("locked", true)

            item.ip?.let { ips ->
                val ipArr = JSONArray()
                ips.forEach { ipArr.put(it) }
                obj.put("ip", ipArr)
            }
            item.domain?.let { doms ->
                val domArr = JSONArray()
                doms.forEach { domArr.put(it) }
                obj.put("domain", domArr)
            }
            item.process?.let { procs ->
                val procArr = JSONArray()
                procs.forEach { procArr.put(it) }
                obj.put("process", procArr)
            }
            array.put(obj)
        }
        return array.toString(2)
    }

    private fun String?.isNullEmpty(): Boolean = this == null || this.trim().isEmpty()
}
