package bypass.whitelist.iran.util

import java.security.SecureRandom

object Ports {
    const val DEFAULT_SOCKS = 1080L
    const val DEFAULT_LOCAL_DNS = 10853L
}

enum class SocksAuthMode { AUTO, MANUAL }

object SocksAuth {
    private val autoUser: String
    private val autoPass: String

    init {
        val random = SecureRandom()
        val chars = "abcdefghijklmnopqrstuvwxyz0123456789"
        fun randomString(length: Int) = buildString {
            repeat(length) { append(chars[random.nextInt(chars.length)]) }
        }
        autoUser = randomString(16)
        autoPass = randomString(24)
    }

    val user: String
        get() = if (Prefs.socksAuthMode == SocksAuthMode.MANUAL) Prefs.socksUser else autoUser

    val pass: String
        get() = if (Prefs.socksAuthMode == SocksAuthMode.MANUAL) Prefs.socksPass else autoPass
}

enum class DnsMode(val label: String) {
    SYSTEM("System"),
    CUSTOM("Custom"),
}

object PrefsKeys {
    const val CONNECT_ON_START = "connect_on_start"
    const val URL = "url"
    const val TUNNEL_MODE = "tunnel_mode"
    const val SHOW_LOGS = "show_logs"
    const val SPLIT_TUNNELING_MODE = "split_tunneling_mode"
    const val SPLIT_TUNNELING_PACKAGES = "split_tunneling_packages"
    const val USE_CUSTOM_NAME = "use_custom_name"
    const val DISPLAY_NAME = "display_name"
    const val VP8_FPS = "vp8_fps"
    const val VP8_BATCH = "vp8_batch"
    const val VP8_IDLE_FPS = "vp8_idle_fps"
    const val MTU = "mtu"
    const val SOCKS_PORT = "socks_port"
    const val SOCKS_AUTH_MODE = "socks_auth_mode"
    const val SOCKS_USER = "socks_user"
    const val SOCKS_PASS = "socks_pass"
    const val PROXY_ONLY = "proxy_only"
    const val DNS_MODE = "dns_mode"
    const val DNS_PRIMARY = "dns_primary"
    const val DNS_SECONDARY = "dns_secondary"

    // Local DNS & Fake DNS & Routing keys from v2
    const val PREF_LOCAL_DNS_ENABLED = "pref_local_dns_enabled"
    const val PREF_FAKE_DNS_ENABLED = "pref_fake_dns_enabled"
    const val PREF_LOCAL_DNS_PORT = "pref_local_dns_port"
    const val PREF_REMOTE_DNS = "pref_remote_dns"
    const val PREF_DOMESTIC_DNS = "pref_domestic_dns"
    const val PREF_DNS_HOSTS = "pref_dns_hosts"
    const val PREF_VPN_DNS = "pref_vpn_dns"

    const val ROUTING_ENABLED = "routing_enabled"
    const val ROUTING_CONFIG_JSON = "routing_config_json"
    const val ROUTING_MODE = "routing_mode"
    const val ROUTING_CUSTOM_DIRECT = "routing_custom_direct"
    const val ROUTING_CUSTOM_BLOCK = "routing_custom_block"
    const val ROUTING_CUSTOM_PROXY = "routing_custom_proxy"
    const val PREF_ROUTING_DOMAIN_STRATEGY = "pref_routing_domain_strategy"
    const val PREF_ROUTING_RULESET = "pref_routing_ruleset"
}

object VP8Defaults {
    const val FPS = 24
    const val BATCH = 30
    const val IDLE_FPS = 1
}

object Vpn {
    const val ADDRESS = "10.0.0.2"
    const val PREFIX_LENGTH = 32
    const val ROUTE = "0.0.0.0"
    const val MTU = 1500
    const val DNS_PRIMARY = "1.1.1.1"
    const val DNS_SECONDARY = "1.0.0.1"
    const val SESSION_NAME = "WhitelistBypass"
    const val PORT_LOCAL_DNS = "10853"
}
