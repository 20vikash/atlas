---
layout: home

hero:
  name: Atlas
  text: VM infrastructure for Frappe Cloud
  tagline: A practical guide to the control plane, host runtime, public proxy, and private network.
  image:
    src: /logo.svg
    alt: Atlas
  actions:
    - theme: brand
      text: Start with the architecture
      link: /docs/architecture
---

## Four systems, one VM platform

Each component has one clear responsibility. Start with the component that owns the behavior you want to change.

<div class="atlas-component-grid">
  <a class="atlas-component" href="./atlas/">
    <span>Control plane</span>
    <strong>Atlas app</strong>
    <p>Stores user intent, selects hosts, and manages provider resources.</p>
  </a>
  <a class="atlas-component" href="./metal/">
    <span>Host runtime</span>
    <strong>Metal</strong>
    <p>Makes each host match the desired virtual machine state.</p>
  </a>
  <a class="atlas-component" href="./services/http-proxy/">
    <span>Public traffic</span>
    <strong>HTTP proxy</strong>
    <p>Routes public HTTP and TLS traffic to virtual machines.</p>
  </a>
  <a class="atlas-component" href="./services/wg-mesh/">
    <span>Private traffic</span>
    <strong>WG Mesh</strong>
    <p>Carries private virtual machine traffic between hosts.</p>
  </a>
</div>

<p class="atlas-home-paths"><a href="./docs/development">Set up development</a><span>or</span><a href="./docs/operations">investigate a failure</a></p>
