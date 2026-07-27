package bypass.whitelist.iran.util

import android.content.Context
import android.content.SharedPreferences
import androidx.core.content.edit
import bypass.whitelist.iran.tunnel.SplitTunnelingMode
import bypass.whitelist.iran.tunnel.TunnelMode

object Prefs {

    private lateinit var prefs: SharedPreferences

    fun init(context: Context) {
        prefs = context.getSharedPreferences("app_prefs", Context.MODE_PRIVATE)
    }

    var connectOnStart: Boolean
        get() = prefs.getBoolean(PrefsKeys.CONNECT_ON_START, false)
        set(value) = prefs.edit { putBoolean(PrefsKeys.CONNECT_ON_START, value) }

    var lastUrl: String
        get() = prefs.getString(PrefsKeys.URL, "")!!
        set(value) = prefs.edit { putString(PrefsKeys.URL, value) }

    var tunnelMode: TunnelMode
        get() {
            val name = prefs.getString(PrefsKeys.TUNNEL_MODE, TunnelMode.VIDEO.name)!!
            return try {
                TunnelMode.valueOf(name)
            } catch (_: IllegalArgumentException) {
                TunnelMode.VIDEO
            }
        }
        set(value) = prefs.edit { putString(PrefsKeys.TUNNEL_MODE, value.name) }

    var showLogs: Boolean
        get() = prefs.getBoolean(PrefsKeys.SHOW_LOGS, false)
        set(value) = prefs.edit { putBoolean(PrefsKeys.SHOW_LOGS, value) }

    var splitTunnelingMode: SplitTunnelingMode
        get() {
            val title = prefs.getString(PrefsKeys.SPLIT_TUNNELING_MODE, SplitTunnelingMode.NONE.name)!!
            return try {
                SplitTunnelingMode.valueOf(title)
            } catch (_: IllegalArgumentException) {
                SplitTunnelingMode.NONE
            }
        }
        set(value) = prefs.edit { putString(PrefsKeys.SPLIT_TUNNELING_MODE, value.name) }

    var splitTunnelingPackages: Set<String>
        get() = prefs.getStringSet(PrefsKeys.SPLIT_TUNNELING_PACKAGES, emptySet()) ?: emptySet()
        set(value) = prefs.edit { putStringSet(PrefsKeys.SPLIT_TUNNELING_PACKAGES, value) }

    var useCustomName: Boolean
        get() = prefs.getBoolean(PrefsKeys.USE_CUSTOM_NAME, true)
        set(value) = prefs.edit { putBoolean(PrefsKeys.USE_CUSTOM_NAME, value) }

    var displayName: String
        get() = prefs.getString(PrefsKeys.DISPLAY_NAME, "Hello")!!
        set(value) = prefs.edit { putString(PrefsKeys.DISPLAY_NAME, value) }

    var vp8Fps: Int
        get() = prefs.getInt(PrefsKeys.VP8_FPS, VP8Defaults.FPS)
        set(value) = prefs.edit { putInt(PrefsKeys.VP8_FPS, value) }

    var vp8Batch: Int
        get() = prefs.getInt(PrefsKeys.VP8_BATCH, VP8Defaults.BATCH)
        set(value) = prefs.edit { putInt(PrefsKeys.VP8_BATCH, value) }

    var vp8IdleFps: Int
        get() = prefs.getInt(PrefsKeys.VP8_IDLE_FPS, VP8Defaults.IDLE_FPS)
        set(value) = prefs.edit { putInt(PrefsKeys.VP8_IDLE_FPS, value) }

    var mtu: Int
        get() = prefs.getInt(PrefsKeys.MTU, Vpn.MTU)
        set(value) = prefs.edit { putInt(PrefsKeys.MTU, value) }

    var socksPort: Long
        get() = prefs.getLong(PrefsKeys.SOCKS_PORT, Ports.DEFAULT_SOCKS)
        set(value) = prefs.edit { putLong(PrefsKeys.SOCKS_PORT, value) }

    var socksAuthMode: SocksAuthMode
        get() {
            val name = prefs.getString(PrefsKeys.SOCKS_AUTH_MODE, SocksAuthMode.AUTO.name)!!
            return try {
                SocksAuthMode.valueOf(name)
            } catch (_: IllegalArgumentException) {
                SocksAuthMode.AUTO
            }
        }
        set(value) = prefs.edit { putString(PrefsKeys.SOCKS_AUTH_MODE, value.name) }

    var socksUser: String
        get() = prefs.getString(PrefsKeys.SOCKS_USER, "")!!
        set(value) = prefs.edit { putString(PrefsKeys.SOCKS_USER, value) }

    var socksPass: String
        get() = prefs.getString(PrefsKeys.SOCKS_PASS, "")!!
        set(value) = prefs.edit { putString(PrefsKeys.SOCKS_PASS, value) }

    var proxyOnly: Boolean
        get() = prefs.getBoolean(PrefsKeys.PROXY_ONLY, false)
        set(value) = prefs.edit { putBoolean(PrefsKeys.PROXY_ONLY, value) }

    var dnsMode: DnsMode
        get() {
            val name = prefs.getString(PrefsKeys.DNS_MODE, DnsMode.CUSTOM.name)!!
            return try {
                DnsMode.valueOf(name)
            } catch (_: IllegalArgumentException) {
                DnsMode.CUSTOM
            }
        }
        set(value) = prefs.edit { putString(PrefsKeys.DNS_MODE, value.name) }

    var dnsPrimary: String
        get() = prefs.getString(PrefsKeys.DNS_PRIMARY, Vpn.DNS_PRIMARY)!!
        set(value) = prefs.edit { putString(PrefsKeys.DNS_PRIMARY, value) }

    var dnsSecondary: String
        get() = prefs.getString(PrefsKeys.DNS_SECONDARY, Vpn.DNS_SECONDARY)!!
        set(value) = prefs.edit { putString(PrefsKeys.DNS_SECONDARY, value) }

    // --- Local DNS & Fake DNS Preferences ---

    var localDnsEnabled: Boolean
        get() = prefs.getBoolean(PrefsKeys.PREF_LOCAL_DNS_ENABLED, false)
        set(value) = prefs.edit { putBoolean(PrefsKeys.PREF_LOCAL_DNS_ENABLED, value) }

    var fakeDnsEnabled: Boolean
        get() = prefs.getBoolean(PrefsKeys.PREF_FAKE_DNS_ENABLED, false)
        set(value) = prefs.edit { putBoolean(PrefsKeys.PREF_FAKE_DNS_ENABLED, value) }

    var localDnsPort: String
        get() = prefs.getString(PrefsKeys.PREF_LOCAL_DNS_PORT, Vpn.PORT_LOCAL_DNS) ?: Vpn.PORT_LOCAL_DNS
        set(value) = prefs.edit { putString(PrefsKeys.PREF_LOCAL_DNS_PORT, value) }

    var remoteDns: String
        get() = prefs.getString(PrefsKeys.PREF_REMOTE_DNS, "https://1.1.1.1/dns-query") ?: "https://1.1.1.1/dns-query"
        set(value) = prefs.edit { putString(PrefsKeys.PREF_REMOTE_DNS, value) }

    var domesticDns: String
        get() = prefs.getString(PrefsKeys.PREF_DOMESTIC_DNS, "223.5.5.5,119.29.29.29") ?: "223.5.5.5,119.29.29.29"
        set(value) = prefs.edit { putString(PrefsKeys.PREF_DOMESTIC_DNS, value) }

    var dnsHosts: String
        get() = prefs.getString(PrefsKeys.PREF_DNS_HOSTS, "") ?: ""
        set(value) = prefs.edit { putString(PrefsKeys.PREF_DNS_HOSTS, value) }

    var vpnDns: String
        get() = prefs.getString(PrefsKeys.PREF_VPN_DNS, "") ?: ""
        set(value) = prefs.edit { putString(PrefsKeys.PREF_VPN_DNS, value) }

    // --- Routing Preferences ---

    var routingEnabled: Boolean
        get() = prefs.getBoolean(PrefsKeys.ROUTING_ENABLED, true)
        set(value) = prefs.edit { putBoolean(PrefsKeys.ROUTING_ENABLED, value) }

    var routingDomainStrategy: String
        get() = prefs.getString(PrefsKeys.PREF_ROUTING_DOMAIN_STRATEGY, "AsIs") ?: "AsIs"
        set(value) = prefs.edit { putString(PrefsKeys.PREF_ROUTING_DOMAIN_STRATEGY, value) }

    var routingRuleset: String
        get() = prefs.getString(PrefsKeys.PREF_ROUTING_RULESET, "") ?: ""
        set(value) = prefs.edit { putString(PrefsKeys.PREF_ROUTING_RULESET, value) }

    var routingConfigJson: String
        get() = prefs.getString(PrefsKeys.ROUTING_CONFIG_JSON, """{
  "domainStrategy": "AsIs",
  "rules": [
    {
      "outboundTag": "direct",
      "domain": [
        "domain:ir",
        "full:bale.ai"
      ]
    },
    {
      "outboundTag": "direct",
      "ip": [
        "geoip:private",
        "geoip:ir"
      ]
    },
    {
      "outboundTag": "block",
      "domain": [
        "geosite:category-ads-all"
      ]
    },
    {
      "outboundTag": "block",
      "network": [
        "udp"
      ],
      "port": [
        "443"
      ]
    }
  ]
}""")!!
        set(value) = prefs.edit { putString(PrefsKeys.ROUTING_CONFIG_JSON, value) }

    var routingMode: String
        get() = prefs.getString(PrefsKeys.ROUTING_MODE, "BYPASS_LAN_IRAN")!!
        set(value) = prefs.edit { putString(PrefsKeys.ROUTING_MODE, value) }

    var routingCustomDirect: String
        get() = prefs.getString(PrefsKeys.ROUTING_CUSTOM_DIRECT, "domain:ir\ngeoip:private\ngeoip:ir\nfull:bale.ai")!!
        set(value) = prefs.edit { putString(PrefsKeys.ROUTING_CUSTOM_DIRECT, value) }

    var routingCustomBlock: String
        get() = prefs.getString(PrefsKeys.ROUTING_CUSTOM_BLOCK, "udp:443\ngeosite:category-ads-all")!!
        set(value) = prefs.edit { putString(PrefsKeys.ROUTING_CUSTOM_BLOCK, value) }

    var routingCustomProxy: String
        get() = prefs.getString(PrefsKeys.ROUTING_CUSTOM_PROXY, "")!!
        set(value) = prefs.edit { putString(PrefsKeys.ROUTING_CUSTOM_PROXY, value) }
}
