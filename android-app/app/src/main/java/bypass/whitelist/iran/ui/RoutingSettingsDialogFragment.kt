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
import bypass.whitelist.iran.enums.RoutingType
import bypass.whitelist.iran.util.Prefs
import bypass.whitelist.iran.util.SettingsManager
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
        val domainStrategySpinner = view.findViewById<Spinner>(R.id.domainStrategySpinner)
        val presetRulesetSpinner = view.findViewById<Spinner>(R.id.presetRulesetSpinner)
        val customContainer = view.findViewById<LinearLayout>(R.id.customRulesContainer)
        val tabLayout = view.findViewById<TabLayout>(R.id.customRulesTabLayout)
        val customDirectInput = view.findViewById<EditText>(R.id.customDirectInput)
        val customBlockedInput = view.findViewById<EditText>(R.id.customBlockedInput)
        val customProxyInput = view.findViewById<EditText>(R.id.customProxyInput)
        val hintText = view.findViewById<TextView>(R.id.customRulesHint)

        // Set up Domain Strategy Spinner
        val strategies = resources.getStringArray(R.array.routing_domain_strategy)
        val strategyAdapter = ArrayAdapter(requireContext(), android.R.layout.simple_spinner_item, strategies)
        strategyAdapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item)
        domainStrategySpinner.adapter = strategyAdapter

        val initialStrategyIndex = strategies.indexOf(Prefs.routingDomainStrategy).let { if (it >= 0) it else 0 }
        domainStrategySpinner.setSelection(initialStrategyIndex)

        // Set up Preset Rulesets Spinner
        val presetNames = arrayOf(
            "Bypass LAN & Mainland (White)",
            "Global Proxy (Black)",
            "Global (All Proxy)",
            "Bypass LAN & Iran (White Iran)",
            "Bypass LAN & Russia (White Russia)",
            "Custom Rules"
        )
        val presetAdapter = ArrayAdapter(requireContext(), android.R.layout.simple_spinner_item, presetNames)
        presetAdapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item)
        presetRulesetSpinner.adapter = presetAdapter

        val initialPresetIndex = when (Prefs.routingMode) {
            "WHITE" -> 0
            "BLACK" -> 1
            "GLOBAL" -> 2
            "BYPASS_LAN_IRAN", "WHITE_IRAN" -> 3
            "WHITE_RUSSIA" -> 4
            "CUSTOM" -> 5
            else -> 3
        }
        presetRulesetSpinner.setSelection(initialPresetIndex)

        enableCheckbox.isChecked = Prefs.routingEnabled
        customDirectInput.setText(Prefs.routingCustomDirect)
        customBlockedInput.setText(Prefs.routingCustomBlock)
        customProxyInput.setText(Prefs.routingCustomProxy)

        fun updateContainerVisibility() {
            settingsContainer.visibility = if (enableCheckbox.isChecked) View.VISIBLE else View.GONE
        }
        enableCheckbox.setOnCheckedChangeListener { _, _ -> updateContainerVisibility() }
        updateContainerVisibility()

        presetRulesetSpinner.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
            override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) {
                customContainer.visibility = if (position == 5) View.VISIBLE else View.GONE
            }
            override fun onNothingSelected(parent: AdapterView<*>?) {}
        }
        customContainer.visibility = if (initialPresetIndex == 5) View.VISIBLE else View.GONE

        // Custom rules tabs
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

                val selectedStrategy = strategies[domainStrategySpinner.selectedItemPosition]
                Prefs.routingDomainStrategy = selectedStrategy

                val selectedPresetIndex = presetRulesetSpinner.selectedItemPosition
                val modeStr = when (selectedPresetIndex) {
                    0 -> "WHITE"
                    1 -> "BLACK"
                    2 -> "GLOBAL"
                    3 -> "WHITE_IRAN"
                    4 -> "WHITE_RUSSIA"
                    5 -> "CUSTOM"
                    else -> "WHITE_IRAN"
                }
                Prefs.routingMode = modeStr

                Prefs.routingCustomDirect = customDirectInput.text.toString()
                Prefs.routingCustomBlock = customBlockedInput.text.toString()
                Prefs.routingCustomProxy = customProxyInput.text.toString()

                Prefs.routingConfigJson = generateRoutingJson(
                    selectedStrategy,
                    selectedPresetIndex,
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
        strategy: String,
        presetIndex: Int,
        customDirect: String,
        customBlock: String,
        customProxy: String
    ): String {
        val root = JSONObject()
        root.put("domainStrategy", strategy)

        if (presetIndex in 0..4) {
            val rulesets = SettingsManager.getPresetRoutingRulesets(requireContext(), presetIndex)
            if (rulesets != null) {
                val rulesArray = JSONArray()
                for (item in rulesets) {
                    if (!item.enabled) continue
                    val rule = JSONObject()
                    rule.put("outboundTag", item.outboundTag)
                    item.port?.let { p ->
                        val pArr = JSONArray()
                        pArr.put(p)
                        rule.put("port", pArr)
                    }
                    item.network?.let { n ->
                        val nArr = JSONArray()
                        nArr.put(n)
                        rule.put("network", nArr)
                    }
                    item.ip?.let { ips ->
                        val ipArr = JSONArray()
                        ips.forEach { ipArr.put(it) }
                        rule.put("ip", ipArr)
                    }
                    item.domain?.let { doms ->
                        val domArr = JSONArray()
                        doms.forEach { domArr.put(it) }
                        rule.put("domain", domArr)
                    }
                    rulesArray.put(rule)
                }
                root.put("rules", rulesArray)
                return root.toString(2)
            }
        }

        // Custom rules fallback
        val rulesArray = JSONArray()
        fun addCustomRule(tag: String, text: String) {
            val lines = text.split("\n").map { it.trim() }.filter { it.isNotEmpty() }
            if (lines.isEmpty()) return
            val domains = JSONArray()
            val ips = JSONArray()
            val ports = JSONArray()
            val networks = JSONArray()

            for (line in lines) {
                when {
                    line.startsWith("udp:") -> {
                        networks.put("udp")
                        ports.put(line.substringAfter("udp:"))
                    }
                    line.startsWith("tcp:") -> {
                        networks.put("tcp")
                        ports.put(line.substringAfter("tcp:"))
                    }
                    line.startsWith("geoip:") || line.matches(Regex("""^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(/\d+)?$""")) -> ips.put(line)
                    else -> domains.put(line)
                }
            }

            if (domains.length() > 0 || ips.length() > 0 || ports.length() > 0) {
                val rule = JSONObject().apply {
                    put("outboundTag", tag)
                    if (domains.length() > 0) put("domain", domains)
                    if (ips.length() > 0) put("ip", ips)
                    if (ports.length() > 0) put("port", ports)
                    if (networks.length() > 0) put("network", networks)
                }
                rulesArray.put(rule)
            }
        }

        addCustomRule("direct", customDirect)
        addCustomRule("block", customBlock)
        addCustomRule("proxy", customProxy)

        val defaultBlockRule = JSONObject().apply {
            put("outboundTag", "block")
            put("network", JSONArray().apply { put("udp") })
            put("port", JSONArray().apply { put("443") })
        }
        rulesArray.put(defaultBlockRule)

        root.put("rules", rulesArray)
        return root.toString(2)
    }

    companion object {
        const val TAG = "RoutingSettingsDialog"
    }
}
