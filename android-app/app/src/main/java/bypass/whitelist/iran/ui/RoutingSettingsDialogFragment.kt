package bypass.whitelist.iran.ui

import android.app.Dialog
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.fragment.app.DialogFragment
import bypass.whitelist.iran.R
import bypass.whitelist.iran.util.Prefs
import org.json.JSONObject

class RoutingSettingsDialogFragment : DialogFragment() {

    override fun onCreateDialog(savedInstanceState: Bundle?): Dialog {
        val view = layoutInflater.inflate(R.layout.dialog_routing_settings, null)

        val enableCheckbox = view.findViewById<CheckBox>(R.id.routingEnableCheckbox)
        val configInput = view.findViewById<EditText>(R.id.routingConfigInput)
        val validateButton = view.findViewById<Button>(R.id.routingValidateButton)
        val resetButton = view.findViewById<Button>(R.id.routingResetButton)

        enableCheckbox.isChecked = Prefs.routingEnabled
        configInput.setText(Prefs.routingConfigJson)
        configInput.isEnabled = Prefs.routingEnabled

        enableCheckbox.setOnCheckedChangeListener { _, isChecked ->
            configInput.isEnabled = isChecked
        }

        validateButton.setOnClickListener {
            val jsonText = configInput.text.toString().trim()
            if (jsonText.isEmpty()) {
                Toast.makeText(requireContext(), R.string.routing_invalid_json, Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            try {
                JSONObject(jsonText)
                Toast.makeText(requireContext(), R.string.routing_valid_json, Toast.LENGTH_SHORT).show()
            } catch (e: Exception) {
                Toast.makeText(requireContext(), "${getString(R.string.routing_invalid_json)}: ${e.message}", Toast.LENGTH_LONG).show()
            }
        }

        resetButton.setOnClickListener {
            val template = """{
  "domainStrategy": "IPIfNonMatch",
  "rules": [
    {
      "outboundTag": "direct",
      "domain": [
        "domain:ir",
        "full:bale.ai"
      ],
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
    }
  ]
}"""
            configInput.setText(template)
        }

        return AlertDialog.Builder(requireContext())
            .setTitle(R.string.routing_settings_title)
            .setView(view)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                Prefs.routingEnabled = enableCheckbox.isChecked
                Prefs.routingConfigJson = configInput.text.toString()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .create()
    }

    companion object {
        const val TAG = "RoutingSettingsDialog"
    }
}
