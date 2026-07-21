package bypass.whitelist.iran.ui

import android.app.Dialog
import android.os.Bundle
import android.view.View
import android.widget.AdapterView
import android.widget.ArrayAdapter
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.Spinner
import android.widget.TextView
import androidx.appcompat.app.AlertDialog
import androidx.fragment.app.DialogFragment
import bypass.whitelist.iran.R
import bypass.whitelist.iran.util.Prefs
import com.google.android.material.tabs.TabLayout
import org.json.JSONArray
import org.json.JSONObject

class RoutingSettingsDialogFragment : DialogFragment {

    private var onSaveListener: (() -> Unit)? = null

    constructor() : super()

    constructor(onSaveListener: () -> Unit) : super() {
        this.onSaveListener = onSaveListener
    }

    override fun onCreateDialog(savedInstanceState: Bundle?): Dialog {
        val view = layoutInflater.inflate(R.layout.dialog_routing_settings, null)

        val enableCheckbox = view.findViewById<CheckBox>(R.id.routingEnableCheckbox)
        val settingsContainer = view.findViewById<LinearLayout>(R.id.routingSettingsContainer)
        val modeSpinner = view.findViewById<Spinner>(R.id.routingModeSpinner)
        val customContainer = view.findViewById<LinearLayout>(R.id.customRulesContainer)
        val tabLayout = view.findViewById<TabLayout>(R.id.customRulesTabLayout)
        val customDirectInput = view.findViewById<EditText>(R.id.customDirectInput)
        val customBlockedInput = view.findViewById<EditText>(R.id.customBlockedInput)
        val customProxyInput = view.findViewById<EditText>(R.id.customProxyInput)
        val hintText = view.findViewById<TextView>(R.id.customRulesHint)

        // Set up the Spinner for Routing Modes
        val modes = arrayOf("Global (Proxy All)", "Bypass LAN", "Bypass LAN & Iran", "Custom Rules")
        val adapter = ArrayAdapter(requireContext(), android.R.layout.simple_spinner_item, modes)
        adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item)
        modeSpinner.adapter = adapter

        // Set initial states from preferences
        enableCheckbox.isChecked = Prefs.routingEnabled
        
        val initialIndex = when (Prefs.routingMode) {
            "GLOBAL" -> 0
            "BYPASS_LAN" -> 1
            "BYPASS_LAN_IRAN" -> 2
            "CUSTOM" -> 3
            else -> 2
        }
        modeSpinner.setSelection(initialIndex)

        customDirectInput.setText(Prefs.routingCustomDirect)
        customBlockedInput.setText(Prefs.routingCustomBlock)
        customProxyInput.setText(Prefs.routingCustomProxy)

        // Sync container visibility with enable/disable checkbox
        fun updateContainerVisibility() {
            val isEnabled = enableCheckbox.isChecked
            settingsContainer.visibility = if (isEnabled) View.VISIBLE else View.GONE
        }
        enableCheckbox.setOnCheckedChangeListener { _, _ -> updateContainerVisibility() }
        updateContainerVisibility()

        // Sync custom rules visibility with selected mode
        modeSpinner.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
            override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) {
                customContainer.visibility = if (position == 3) View.VISIBLE else View.GONE
            }
            override fun onNothingSelected(parent: AdapterView<*>?) {}
        }
        customContainer.visibility = if (initialIndex == 3) View.VISIBLE else View.GONE

        // Set up the tabs for Custom Rules
        tabLayout.addTab(tabLayout.newTab().setText("Direct"))
        tabLayout.addTab(tabLayout.newTab().setText("Blocked"))
        tabLayout.addTab(tabLayout.newTab().setText("Proxy"))

        tabLayout.addOnTabSelectedListener(object : TabLayout.OnTabSelectedListener {
            override fun onTabSelected(tab: TabLayout.Tab?) {
                val position = tab?.position ?: 0
                customDirectInput.visibility = if (position == 0) View.VISIBLE else View.GONE
                customBlockedInput.visibility = if (position == 1) View.VISIBLE else View.GONE
                customProxyInput.visibility = if (position == 2) View.VISIBLE else View.GONE

                hintText.text = when (position) {
                    0 -> "Direct (bypass) domains and IPs (one per line, e.g. anjammidam.com, regexp:.*\\.ir$)"
                    1 -> "Blocked domains, IPs, ports (one per line, e.g. udp:443, geosite:category-ads-all)"
                    2 -> "Forced proxy domains and IPs (one per line, e.g. domain:google.com)"
                    else -> ""
                }
            }
            override fun onTabUnselected(tab: TabLayout.Tab?) {}
            override fun onTabReselected(tab: TabLayout.Tab?) {}
        })

        return AlertDialog.Builder(requireContext())
            .setTitle(R.string.routing_settings_title)
            .setView(view)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                Prefs.routingEnabled = enableCheckbox.isChecked

                val selectedMode = when (modeSpinner.selectedItemPosition) {
                    0 -> "GLOBAL"
                    1 -> "BYPASS_LAN"
                    2 -> "BYPASS_LAN_IRAN"
                    3 -> "CUSTOM"
                    else -> "BYPASS_LAN_IRAN"
                }
                Prefs.routingMode = selectedMode

                Prefs.routingCustomDirect = customDirectInput.text.toString()
                Prefs.routingCustomBlock = customBlockedInput.text.toString()
                Prefs.routingCustomProxy = customProxyInput.text.toString()

                // Compile UI input into Go core's routing JSON format
                Prefs.routingConfigJson = generateRoutingJson(
                    selectedMode,
                    Prefs.routingCustomDirect,
                    Prefs.routingCustomBlock,
                    Prefs.routingCustomProxy
                )

                onSaveListener?.invoke()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .create()
    }

    private fun generateRoutingJson(
        mode: String,
        customDirect: String,
        customBlock: String,
        customProxy: String
    ): String {
        val root = JSONObject()
        root.put("domainStrategy", "AsIs")

        val rulesArray = JSONArray()

        val defaultBlockRule = JSONObject().apply {
            put("outboundTag", "block")
            put("network", JSONArray().apply { put("udp") })
            put("port", JSONArray().apply { put("443") })
        }

        when (mode) {
            "GLOBAL" -> {
                rulesArray.put(defaultBlockRule)
            }
            "BYPASS_LAN" -> {
                val rule = JSONObject()
                rule.put("outboundTag", "direct")
                val ipArray = JSONArray()
                ipArray.put("geoip:private")
                rule.put("ip", ipArray)
                rulesArray.put(rule)
                rulesArray.put(defaultBlockRule)
            }
            "BYPASS_LAN_IRAN" -> {
                // Bypass LAN and Iran Domains
                val directDomainRule = JSONObject().apply {
                    put("outboundTag", "direct")
                    put("domain", JSONArray().apply {
                        put("domain:ir")
                        put("full:bale.ai")
                    })
                }
                rulesArray.put(directDomainRule)

                // Bypass LAN and Iran IPs
                val directIpRule = JSONObject().apply {
                    put("outboundTag", "direct")
                    put("ip", JSONArray().apply {
                        put("geoip:private")
                        put("geoip:ir")
                    })
                }
                rulesArray.put(directIpRule)

                // Block known ad category
                val blockRule = JSONObject().apply {
                    put("outboundTag", "block")
                    put("domain", JSONArray().apply { put("geosite:category-ads-all") })
                }
                rulesArray.put(blockRule)
                rulesArray.put(defaultBlockRule)
            }
            "CUSTOM" -> {
                fun addCustomRule(tag: String, text: String) {
                    val lines = text.split("\n")
                        .map { it.trim() }
                        .filter { it.isNotEmpty() }
                    if (lines.isEmpty()) return

                    val rule = JSONObject()
                    rule.put("outboundTag", tag)

                    val domains = JSONArray()
                    val ips = JSONArray()
                    val ports = JSONArray()
                    val networks = JSONArray()

                    for (line in lines) {
                        if (line.startsWith("#") || line.startsWith("//")) continue
                        val lower = line.lowercase()
                        if (lower == "udp:443" || lower == "443/udp") {
                            networks.put("udp")
                            ports.put("443")
                        } else if (lower.startsWith("udp:")) {
                            networks.put("udp")
                            ports.put(line.substring(4).trim())
                        } else if (lower.startsWith("port:")) {
                            ports.put(line.substring(5).trim())
                        } else if (lower.startsWith("network:")) {
                            networks.put(line.substring(8).trim())
                        } else if (lower.startsWith("domain:") || lower.startsWith("full:") || lower.startsWith("regexp:") || lower.startsWith("keyword:") || lower.startsWith("geosite:")) {
                            domains.put(line)
                        } else if (lower.startsWith("geoip:")) {
                            ips.put(line)
                        } else {
                            val isProbablyIp = lower.contains("/") || (lower.any { it.isDigit() } && !lower.any { it in 'a'..'z' || it in 'A'..'Z' })
                            if (isProbablyIp) {
                                ips.put(line)
                            } else {
                                domains.put(line)
                            }
                        }
                    }

                    if (domains.length() > 0) {
                        val dRule = JSONObject()
                        dRule.put("outboundTag", tag)
                        dRule.put("domain", domains)
                        if (ports.length() > 0) dRule.put("port", ports)
                        if (networks.length() > 0) dRule.put("network", networks)
                        rulesArray.put(dRule)
                    }
                    if (ips.length() > 0) {
                        val ipRule = JSONObject()
                        ipRule.put("outboundTag", tag)
                        ipRule.put("ip", ips)
                        if (ports.length() > 0) ipRule.put("port", ports)
                        if (networks.length() > 0) ipRule.put("network", networks)
                        rulesArray.put(ipRule)
                    }
                    if (domains.length() == 0 && ips.length() == 0 && (ports.length() > 0 || networks.length() > 0)) {
                        val otherRule = JSONObject()
                        otherRule.put("outboundTag", tag)
                        if (ports.length() > 0) otherRule.put("port", ports)
                        if (networks.length() > 0) otherRule.put("network", networks)
                        rulesArray.put(otherRule)
                    }
                }

                // Compile block first, then direct, then proxy
                addCustomRule("block", customBlock)
                addCustomRule("direct", customDirect)
                addCustomRule("proxy", customProxy)

                // Ensure default UDP 443 block rule is included if not explicitly present
                var hasUDP443Block = false
                for (i in 0 until rulesArray.length()) {
                    val r = rulesArray.getJSONObject(i)
                    if (r.optString("outboundTag") == "block") {
                        val netArr = r.optJSONArray("network")
                        val portArr = r.optJSONArray("port")
                        if (netArr != null && portArr != null && netArr.length() > 0 && portArr.length() > 0) {
                            if (netArr.getString(0) == "udp" && portArr.getString(0) == "443") {
                                hasUDP443Block = true
                                break
                            }
                        }
                    }
                }
                if (!hasUDP443Block) {
                    val defaultBlockRule = JSONObject()
                    defaultBlockRule.put("outboundTag", "block")
                    val netArr = JSONArray().apply { put("udp") }
                    val portArr = JSONArray().apply { put("443") }
                    defaultBlockRule.put("network", netArr)
                    defaultBlockRule.put("port", portArr)
                    rulesArray.put(defaultBlockRule)
                }
            }
        }

        root.put("rules", rulesArray)
        return root.toString(2)
    }

    companion object {
        const val TAG = "RoutingSettingsDialog"
    }
}
