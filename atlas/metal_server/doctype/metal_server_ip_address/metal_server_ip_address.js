frappe.ui.form.on("Metal Server IP Address", {
	refresh(frm) {
		const isDetached = frm.doc.status === "Allocated" && !frm.doc.virtual_machine;

		if (frappe.user.has_role("System Manager") && isDetached) {
			frm.add_custom_button(__("Reset Tenant"), () => {
				frappe.confirm(
					__("Return {0} to the shared pool? Tenant {1} loses it.", [
						frm.doc.address,
						frm.doc.tenant_id,
					]),
					() => frm.call("reset_tenant").then(() => frm.reload_doc())
				);
			});
		}
	},
});
