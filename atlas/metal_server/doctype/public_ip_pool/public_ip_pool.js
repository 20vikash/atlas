frappe.ui.form.on("Public IP Pool", {
	async refresh(frm) {
		if (!frm.doc.gateway) {
			frm.set_intro(
				__("Metal adds allocations from this pool directly to virtual machines."),
				"blue"
			);
		} else {
			frm.set_intro(
				__("This IPv6 pool is routed through {0}.", [frm.doc.gateway.bold()]),
				"blue"
			);
		}

		if (frm.is_new() || !frappe.user.has_role("System Manager")) return;
		if (!frm.doc.gateway) {
			frm.add_custom_button(
				__("Create Allocation Stock"),
				() => createAllocationStock(frm),
				__("Actions")
			);
		}
		if (["Attaching", "Detaching"].includes(frm.doc.provider_status)) {
			frm.add_custom_button(
				__("Retry Provider Operation"),
				() => frm.call("retry_provider_operation"),
				__("Actions")
			);
		}
		if (frm.doc.source === "Provider") {
			frm.add_custom_button(
				__("Release Provider Pool"),
				() => frm.savetrash(),
				__("Dangerous Actions")
			);
		}
	},
});

function createAllocationStock(frm) {
	return frm
		.call({ method: "create_allocation_stock", doc: frm.doc, freeze: true })
		.then((response) => {
			frappe.show_alert(
				__("Created {0} public IP allocations.", [response.message]),
				response.message ? "green" : "blue"
			);
			frm.reload_doc();
		});
}
