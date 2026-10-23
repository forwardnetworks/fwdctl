# A path that loops through an ACI service redirect (PBR)

Read when a path result repeats the same leaf (a loop) and the network is Cisco ACI with a service graph. Everything here is read with `inspect-device-files` on the leaf that repeats. **Peer-reported recipe, not yet confirmed against Forward's model**: use it to gather evidence, and say what is a reading of files and what is a claim about Forward.

A last hop with no egress interface in a loop result is the loop's repeat marker, not a drop. The result does not say why the packet came back.

1. **Leaf and VRF.** The leaf that repeats in the loop hops. Its `vrf_routing.txt` header names the VRF; `system_internal_policy_manager.txt` lists rows of scope (vnid), VRF name, prefix and pcTag.
2. **Address to pcTag.** Search `system_internal_policy_manager.txt` for the source and the destination subnet, inside the flow's VRF. A pcTag is unique only within a scope, so match the scope column too.
3. **Rules between them.** Search `zoning_rule.txt` for the `src | dst` pcTag pair in both orders, restricted to that scope. Note the action: `redir(destgrp-N)` redirects, `redir_override` on a side changes it, `permit_handoff` hands off.
4. **Redirect target.** `fib_pbr.txt` maps `destgrp-N` to the service node's address (`dest-[ip]`) in that VRF. The service node's own configuration (`configuration.txt` of its context) shows whether it is one-arm (one interface, same-security-traffic permit intra-interface, default route back toward the fabric).
5. **Return from the service node.** In the same scope, look for `permit_handoff` rules whose source pcTag is not in the prefix table for that scope: that class is plausibly the one assigned to traffic returning from the service node, and those rules would let it through without a second redirect.

**Hypothesis to hand to engineering (not proven):** the model sends the packet that returns from the service node back through the original source-to-destination redirect rule instead of applying the return class's permit, so it redirects again and loops. State it as a hypothesis, with the rule lines and the pcTags that support it, never as a finding.

Fields a path result may carry per hop (a tunnel, a PBR redirect) are not exposed today; say so when the answer depends on them.
