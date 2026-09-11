frappe.ui.form.on("Virtual Machine Migration", {
	refresh(frm) {
		if (frm.is_new()) {
			return;
		}

		// Atlas drives this record. A user acts through buttons, not a direct save.
		frm.disable_save();

		frm.add_custom_button(
			__("View Virtual Machine"),
			() => frappe.set_route("Form", "Virtual Machine", frm.doc.virtual_machine),
			__("Actions")
		);

		const is_active = ["running", "ready"].includes(frm.doc.status);
		if (is_active && !frm.doc.abort_requested) {
			frm.add_custom_button(__("Abort Migration"), () => abortMigration(frm), __("Actions"));
		}

		scheduleProgressRefresh(frm);
	},
});

// A running migration reloads on an interval, so the desk shows live progress.
function scheduleProgressRefresh(frm) {
	if (frm.__migration_poll) {
		clearInterval(frm.__migration_poll);
		frm.__migration_poll = null;
	}
	if (!["running", "ready"].includes(frm.doc.status)) {
		return;
	}
	frm.__migration_poll = setInterval(() => {
		const route = frappe.get_route();
		const on_this_form =
			route[0] === "Form" && route[1] === frm.doctype && route[2] === frm.doc.name;
		if (!on_this_form) {
			clearInterval(frm.__migration_poll);
			frm.__migration_poll = null;
			return;
		}
		if (!frm.is_dirty()) {
			frm.reload_doc();
		}
	}, 2000);
}

function abortMigration(frm) {
	frappe.confirm(
		__(
			"Abort this migration? The VM stays on {0} if it has not cut over yet. Abort is not allowed once Atlas commits the VM to the target.",
			[frm.doc.source_server]
		),
		() =>
			frm
				.call({
					method: "abort",
					doc: frm.doc,
					freeze: true,
					freeze_message: __("Requesting abort..."),
				})
				.then(() => frm.reload_doc())
	);
}
