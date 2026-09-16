const UNOWNED_TENANT_ID = -1;

frappe.ui.form.on("Metal Server IP Address", {
	refresh(frm) {
		const is_detached = frm.doc.status === "Allocated" && !frm.doc.virtual_machine;
		const is_owned = frm.doc.tenant_id !== UNOWNED_TENANT_ID;

		[
			[
				__("Reserve"),
				() => frm.call("reserve").then(() => frm.reload_doc()),
				is_owned && !frm.doc.reserved && frm.doc.status !== "Detaching",
				__("Keep {0} for tenant {1}? A detach no longer returns it to the shared pool.", [
					frm.doc.address.bold(),
					frm.doc.tenant_id,
				]),
				false,
			],
			[
				__("Reset Tenant"),
				() => frm.call("reset_tenant").then(() => frm.reload_doc()),
				is_detached && is_owned && frappe.user.has_role("System Manager"),
				__("Return {0} to the shared pool? Tenant {1} loses it.", [
					frm.doc.address.bold(),
					frm.doc.tenant_id,
				]),
				false,
			],
			[
				__("Remove"),
				// savetrash confirms and releases the provider reservation.
				() => frm.savetrash(),
				is_detached && !frm.is_new() && frappe.model.can_delete(frm.doctype),
				null,
				true,
			],
		].forEach(([label, action, condition, confirm_message, is_dangerous]) => {
			if (!condition) {
				return;
			}

			frm.add_custom_button(
				label,
				() => (confirm_message ? frappe.confirm(confirm_message, action) : action()),
				is_dangerous ? __("Dangerous Actions") : __("Actions")
			);
		});
	},
});
