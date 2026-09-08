local auto_proxy = {}

-- Slicing the hex keeps a 64-bit VM ID exact, which a Lua number cannot hold.
local function format_hextets(hexadecimal_value, digit_count)
	local padded = string.rep("0", digit_count - #hexadecimal_value) .. hexadecimal_value
	local parts = {}
	for index = 1, digit_count, 4 do
		parts[#parts + 1] = (padded:sub(index, index + 3):gsub("^0+(%x)", "%1"))
	end
	return table.concat(parts, ":")
end

-- The label carries the tenant and the VM as hex field values.
function auto_proxy.get_virtual_machine_address(subdomain, address_prefix, host_prefixes)
	if not address_prefix or address_prefix == "" then
		return nil
	end

	local host_prefix, tenant_id, virtual_machine_id = subdomain:match("^(.-)(%x+)%-(%x+)$")
	if not tenant_id or #tenant_id > 8 or #virtual_machine_id > 16 then
		return nil
	end

	local is_allowed = false
	for _, configured_prefix in ipairs(host_prefixes or {}) do
		if host_prefix == configured_prefix then
			is_allowed = true
			break
		end
		if configured_prefix:sub(1, 1) == "*" then
			local suffix = configured_prefix:sub(2)
			if #host_prefix > #suffix and host_prefix:sub(-#suffix) == suffix then
				is_allowed = true
				break
			end
		end
	end

	if not is_allowed then
		return nil
	end

	local tenant_hextets = format_hextets(tenant_id, 8)
	local virtual_machine_hextets = format_hextets(virtual_machine_id, 16)
	return address_prefix .. ":" .. tenant_hextets .. ":" .. virtual_machine_hextets
end

return auto_proxy
