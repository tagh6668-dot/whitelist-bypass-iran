package bypass.whitelist.iran.ui

import android.app.Dialog
import android.os.Bundle
import android.widget.EditText
import androidx.appcompat.app.AlertDialog
import androidx.fragment.app.DialogFragment
import bypass.whitelist.iran.R
import bypass.whitelist.iran.util.Prefs

class Vp8PacingSettingsDialogFragment : DialogFragment() {

    override fun onCreateDialog(savedInstanceState: Bundle?): Dialog {
        val view = layoutInflater.inflate(R.layout.dialog_vp8_pacing_settings, null)

        val fpsInput = view.findViewById<EditText>(R.id.vp8FpsInput)
        val batchInput = view.findViewById<EditText>(R.id.vp8BatchInput)
        val idleFpsInput = view.findViewById<EditText>(R.id.vp8IdleFpsInput)
        val mtuInput = view.findViewById<EditText>(R.id.mtuInput)

        fpsInput.setText(Prefs.vp8Fps.toString())
        batchInput.setText(Prefs.vp8Batch.toString())
        idleFpsInput.setText(Prefs.vp8IdleFps.toString())
        mtuInput.setText(Prefs.mtu.toString())

        return AlertDialog.Builder(requireContext())
            .setTitle(R.string.vp8_pacing_title)
            .setView(view)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val fps = fpsInput.text.toString().toIntOrNull()
                if (fps != null && fps in 1..240) Prefs.vp8Fps = fps
                val batch = batchInput.text.toString().toIntOrNull()
                if (batch != null && batch in 1..256) Prefs.vp8Batch = batch
                val idleFps = idleFpsInput.text.toString().toIntOrNull()
                if (idleFps != null && idleFps in 1..240) Prefs.vp8IdleFps = idleFps
                val mtuValue = mtuInput.text.toString().toIntOrNull()
                if (mtuValue != null && mtuValue in 576..1500) Prefs.mtu = mtuValue
            }
            .setNegativeButton(android.R.string.cancel, null)
            .create()
    }

    companion object {
        const val TAG = "Vp8PacingSettingsDialog"
    }
}
