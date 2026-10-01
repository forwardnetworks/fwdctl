# Why model a synthetic device

A synthetic device stands in for a segment Forward cannot collect (the internet, a provider core, a partner network, a circuit). Forward answers by following packets through its model; where
the model has nothing to forward through, the answer stops, and a stopped path looks like a failure when it is only a gap.

Modelling it lets these work across the segment:
- **Reachability and troubleshooting:** a flow over an MPLS core or to the internet no longer reads as unreachable or unknown at the edge.
- **Security and exposure:** "what can the internet reach" and partner traffic need the internet node and adjacent networks; without them exposure analysis stops at the perimeter and understates.
- **Segmentation and intent checks:** a flow that must work, or be blocked, end to end across the WAN.
- **Change verification:** predicting a change at one site's edge needs the far side modelled, even coarsely.

The cost: it is a statement you make, not something Forward observed. Results through it are only as true as the description. Use one only for a segment you cannot collect; if a device is reachable and
you have credentials, collect it. Prefer discovered subnets (from the gateway's routes or BGP) or a query over a typed list, so the model follows the network. Source: Forward's "Synthetic Devices"
documentation, paraphrased.
