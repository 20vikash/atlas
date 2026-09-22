function showReserveServerIPAddressDialog() {
	const dialog = new frappe.ui.Dialog({
		title: __("Reserve Public IP"),
		fields: [
			{
				fieldname: "version",
				fieldtype: "Select",
				label: __("Version"),
				options: "4\n6",
				default: "4",
				reqd: 1,
				description: __(
					"IPv4 adds one address to the shared pool for tenant claims. IPv6 adds one block that you attach to a VM."
				),
			},
		],
		primary_action_label: __("Reserve"),
		primary_action(values) {
			frappe.call({
				method: "atlas.metal_server.doctype.metal_server_ip_address.metal_server_ip_address.reserve_for_pool",
				args: { version: values.version },
				freeze: true,
				freeze_message: __("Reserving Public IP"),
				callback(response) {
					dialog.hide();
					frappe.set_route("Form", "Metal Server IP Address", response.message);
				},
			});
		},
	});
	dialog.show();
}

frappe.listview_settings["Metal Server IP Address"] = {
	async onload(listview) {
		listview.server_provider = await frappe.db.get_single_value(
			"Atlas Settings",
			"server_provider"
		);
		listview.refresh();
	},
	refresh(listview) {
		// Generic addresses are added by hand with the standard Add button.
		if (!listview.server_provider || listview.server_provider === "Generic") return;

		listview.page.clear_primary_action();
		if (!has_common(frappe.user_roles, ["System Manager"])) return;
		listview.page.add_inner_button(__("Reserve Public IP"), showReserveServerIPAddressDialog);
	},
};
