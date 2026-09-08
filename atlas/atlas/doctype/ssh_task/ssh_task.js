// Copyright (c) 2026, Frappe and contributors
// For license information, please see license.txt

frappe.ui.form.on("SSH Task", {
	refresh(frm) {
		frappe.realtime.off("ssh_task_output_update");
		frappe.realtime.on("ssh_task_output_update", (message) => {
			if (message.name == frm.doc.name) {
				frm.set_value("output", message.output);
			}
		});
	},
});
