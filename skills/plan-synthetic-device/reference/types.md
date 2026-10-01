# Which synthetic device type

Source: Forward's documentation, "Synthetic Devices" (overview, choosing a device, configuring connections, automated setup). Paraphrased; Forward's pages are the authority.

| The segment is... | Use | What it does |
|---|---|---|
| the public internet | **Internet node** | one per network (name `internet`); routes public addresses only; owns every public address not found elsewhere once it has a connection. What a connection is, and what to do when the internet VRF is already an L3 VPN connection: `internet-node.md` in this folder (read it from the skill step, not from here) |
| a private carrier network of your own (leased L3, hub and spoke) | **Intranet node** | transit hub; routes public and private addresses, only for subnets on its connections; has a self interface that absorbs traffic it cannot route to a connection |
| a provider's L3 VPN / MPLS core whose PE and P routers you cannot collect | **L3 VPN** | transit only; forwards between connections in the same VRF and drops what it cannot route, like a provider core; the VRF names the provider-side VPN |
| a network you connect to but do not manage, that terminates your traffic | **Adjacent network** | traffic sink: reached and returned from, never forwards from one connection to another (a partner, a counterparty, a payment network); no VRF |
| one L2 broadcast domain stretched across a WAN | **L2 VPN** | many endpoints, one broadcast domain; connections are edge interfaces |
| a single point-to-point L2 link through a provider | **WAN circuit** | exactly two endpoints; not definable by an NQE query through the API |
| traffic encrypted into an IPSec tunnel | **Encryptor** | models the tunnel; REST API only, not definable by an NQE query |

Deciding questions: is the segment **L2 or L3**? If L3, **public or private** addressing, and does it carry your traffic **onward between your sites** (transit: intranet node, L3 VPN) or only **terminate**
it (adjacent network)? Ask what the far network does with a packet not addressed to it: forwards it (transit) or accepts only its own (sink). Intranet node versus L3 VPN: your own private routing between
sites versus a provider's opaque core. Use an internet node and an intranet node together when some sites connect over the internet and others over a private network.
