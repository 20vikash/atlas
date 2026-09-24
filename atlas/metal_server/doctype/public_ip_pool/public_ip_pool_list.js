function reserveProviderPool() {
	const dialog = new frappe.ui.Dialog({
		title: __("Reserve Public IP Pool"),
		fields: [
			{
				fieldname: "version",
				fieldtype: "Select",
				label: __("Version"),
				options: "4\n6",
				default: "4",
				reqd: 1,
			},
		],
		primary_action_label: __("Reserve"),
		primary_action(values) {
			frappe.call({
				method: "atlas.metal_server.doctype.public_ip_pool.public_ip_pool.reserve_from_provider",
				args: { version: values.version },
				freeze: true,
				callback(response) {
					dialog.hide();
					frappe.set_route("Form", "Public IP Pool", response.message);
				},
			});
		},
	});
	dialog.show();
}

function setProviderPrimaryAction(listview) {
	if (!listview.serverProvider || !frappe.user.has_role("System Manager")) return;
	if (listview.serverProvider === "Generic") return;

	listview.page.set_primary_action(
		{ label: __("Reserve Public IP Pool"), short_label: __("Reserve") },
		reserveProviderPool,
		"plus"
	);
}

frappe.listview_settings["Public IP Pool"] = {
	async onload(listview) {
		listview.serverProvider = await frappe.db.get_single_value(
			"Atlas Settings",
			"server_provider"
		);
		listview.refresh();
	},
	refresh: setProviderPrimaryAction,
};
