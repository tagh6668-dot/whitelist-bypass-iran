package bypass.whitelist.iran.ui

import android.app.Dialog
import android.os.Bundle
import android.view.View
import android.widget.CheckBox
import android.widget.EditText
import androidx.appcompat.app.AlertDialog
import androidx.fragment.app.DialogFragment
import bypass.whitelist.iran.R
import bypass.whitelist.iran.util.Prefs

class DnsSettingsDialogFragment : DialogFragment {

    private var onSaveListener: (() -> Unit)? = null

    constructor() : super()

    constructor(onSaveListener: () -> Unit) : super() {
        this.onSaveListener = onSaveListener
    }

    override fun onCreateDialog(savedInstanceState: Bundle?): Dialog {
        val view = layoutInflater.inflate(R.layout.dialog_dns_settings, null)

        val cbLocalDns = view.findViewById<CheckBox>(R.id.cbLocalDns)
        val cbFakeDns = view.findViewById<CheckBox>(R.id.cbFakeDns)
        val etRemoteDns = view.findViewById<EditText>(R.id.etRemoteDns)
        val etDomesticDns = view.findViewById<EditText>(R.id.etDomesticDns)
        val etLocalDnsPort = view.findViewById<EditText>(R.id.etLocalDnsPort)
        val etDnsHosts = view.findViewById<EditText>(R.id.etDnsHosts)
        val etVpnDns = view.findViewById<EditText>(R.id.etVpnDns)

        cbLocalDns.isChecked = Prefs.localDnsEnabled
        cbFakeDns.isChecked = Prefs.fakeDnsEnabled
        cbFakeDns.isEnabled = Prefs.localDnsEnabled

        etRemoteDns.setText(Prefs.remoteDns)
        etDomesticDns.setText(Prefs.domesticDns)
        etLocalDnsPort.setText(Prefs.localDnsPort)
        etDnsHosts.setText(Prefs.dnsHosts)
        etVpnDns.setText(Prefs.vpnDns)

        cbLocalDns.setOnCheckedChangeListener { _, isChecked ->
            cbFakeDns.isEnabled = isChecked
            if (!isChecked) {
                cbFakeDns.isChecked = false
            }
        }

        return AlertDialog.Builder(requireContext())
            .setTitle(R.string.dns_settings_title)
            .setView(view)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                Prefs.localDnsEnabled = cbLocalDns.isChecked
                Prefs.fakeDnsEnabled = cbFakeDns.isChecked
                Prefs.remoteDns = etRemoteDns.text.toString().trim()
                Prefs.domesticDns = etDomesticDns.text.toString().trim()
                Prefs.localDnsPort = etLocalDnsPort.text.toString().trim().ifEmpty { "10853" }
                Prefs.dnsHosts = etDnsHosts.text.toString().trim()
                Prefs.vpnDns = etVpnDns.text.toString().trim()

                onSaveListener?.invoke()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .create()
    }

    companion object {
        const val TAG = "DnsSettingsDialog"
    }
}
