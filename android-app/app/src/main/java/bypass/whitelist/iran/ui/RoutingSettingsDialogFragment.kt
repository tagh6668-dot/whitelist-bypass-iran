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
                    0 -> "Direct (bypass) domains and IPs (one per line, e.g. domain:ir, geoip:private)"
                    1 -> "Blocked domains and IPs (one per line, e.g. geosite:category-ads-all)"
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

        when (mode) {
            "GLOBAL" -> {
                // Empty rules array, everything defaults to proxy
            }
            "BYPASS_LAN" -> {
                val rule = JSONObject()
                rule.put("outboundTag", "direct")
                val ipArray = JSONArray()
                ipArray.put("geoip:private")
                rule.put("ip", ipArray)
                rulesArray.put(rule)
            }
            "BYPASS_LAN_IRAN" -> {
                // Bypass LAN and Iran
                val directRule = JSONObject()
                directRule.put("outboundTag", "direct")

                val domainArray = JSONArray()
                domainArray.put("domain:ir")
                domainArray.put("full:bale.ai")
                directRule.put("domain", domainArray)

                val ipArray = JSONArray()
                ipArray.put("geoip:private")
                ipArray.put("geoip:ir")
                directRule.put("ip", ipArray)

                rulesArray.put(directRule)

                // Block known ad category
                val blockRule = JSONObject()
                blockRule.put("outboundTag", "block")
                val blockDomainArray = JSONArray()
                blockDomainArray.put("geosite:category-ads-all")
                blockRule.put("domain", blockDomainArray)

                rulesArray.put(blockRule)
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

                    for (line in lines) {
                        if (line.startsWith("#") || line.startsWith("//")) continue
                        val lower = line.lowercase()
                        if (lower.startsWith("domain:") || lower.startsWith("full:") || lower.startsWith("regexp:") || lower.startsWith("keyword:") || lower.startsWith("geosite:")) {
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
                        rule.put("domain", domains)
                    }
                    if (ips.length() > 0) {
                        rule.put("ip", ips)
                    }

                    rulesArray.put(rule)
                }

                // Compile block first, then direct, then proxy
                addCustomRule("block", customBlock)
                addCustomRule("direct", customDirect)
                addCustomRule("proxy", customProxy)
            }
        }

        root.put("rules", rulesArray)
        return root.toString(2)
    }

    companion object {
        const val TAG = "RoutingSettingsDialog"
    }
}
