frappe.ui.form.on("Virtual Machine Image", {
	refresh(frm) {
		if (frm.doc.image_type === "machine" && frm.doc.status === "Failed") {
			frm.add_custom_button(__("Retry Transfer"), () => {
				frm.call("retry_transfer").then(() => frm.reload_doc());
			});
		}

		if (frm.doc.artifact_storage === "Site File" && frm.doc.status === "Available") {
			frm.add_custom_button(__("Migrate to Object Storage"), () => {
				frm.call("migrate_to_object_storage").then(() => frm.reload_doc());
			});
		}
	},
});
