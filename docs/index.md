---
layout: home

hero:
  name: Atlas
  text: Developer handbook
  tagline: How Frappe Cloud builds, runs, and connects virtual machines.
  image:
    src: /logo.svg
    alt: Atlas
  actions:
    - theme: brand
      text: Start reading
      link: /docs/start/what-atlas-is
---

## From a request to a usable VM

<div class="atlas-home-flow">
  <div class="atlas-home-flow-step">
    <a href="./docs/start/how-a-vm-request-works">Atlas app</a>
    <p>Records the request and selects a host.</p>
  </div>
  <div class="atlas-home-flow-step">
    <a href="./docs/compute/runtime">Metal</a>
    <p>Creates the disk and runs the VM with Firecracker.</p>
  </div>
  <div class="atlas-home-flow-step">
    <a href="./docs/networking/">WG Mesh</a>
    <p>Connects VMs across hosts while their addresses stay fixed.</p>
  </div>
</div>

<p class="atlas-home-services"><a href="./docs/region/service-vms">Regional services</a> use privileged VMs for web routing, public IPv6, and storage.</p>
