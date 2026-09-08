local pages = require("pages")
local auto_proxy = require("auto_proxy")

local sites = ngx.shared.sites

local host = ngx.var.host or ""
-- The region file contains the complete zone, not only the region label.
host = host:lower():gsub(":%d+$", "")

local subdomain
if atlas_root_domain and atlas_root_domain ~= "" then
	local suffix = "." .. atlas_root_domain
	if host:sub(-#suffix) == suffix then
		subdomain = host:sub(1, #host - #suffix)
	end
else
	subdomain = host:match("^([^.]+)%.")
end

if not subdomain or subdomain == "" then
	return pages.serve("not_found", ngx.HTTP_NOT_FOUND)
end

-- Route reserved control subdomains before the site map.
if atlas_control_subdomains and atlas_control_subdomains[subdomain] then
	ngx.var.vm_upstream = "http://127.0.0.1:9000"
	return
end

local virtual_machine_address = auto_proxy.get_virtual_machine_address(
	subdomain,
	atlas_auto_proxy_address_prefix,
	atlas_auto_proxy_host_prefixes
)
if virtual_machine_address then
	ngx.var.vm_upstream = "http://[" .. virtual_machine_address .. "]:80"
	return
end

local address = sites:get(subdomain)
if not address then
	return pages.serve("not_found", ngx.HTTP_NOT_FOUND)
end

if address == "-" then
	return pages.serve("not_found", ngx.HTTP_SERVICE_UNAVAILABLE)
end

ngx.var.vm_upstream = "http://[" .. address .. "]:80"
