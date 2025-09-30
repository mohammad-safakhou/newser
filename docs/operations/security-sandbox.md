# Sandbox Evaluation (NSJail vs Docker)

## Current executor context
- The executor service launches agent tasks (research, analysis, synthesis) but currently acts as a placeholder implementation.
- Future designs must safely run code snippets and external tools without jeopardizing the host or leaking secrets.

## Candidate runtimes
### Docker container sandbox
- **Pros**: widely available; native resource controls (CPU, memory, network) via cgroups; easy to clean filesystem via ephemeral containers; matches existing containerized deployment.
- **Cons**: requires Docker daemon access which may be over-privileged; startup latency for each invocation unless reusing pools.

### NSJail
- **Pros**: Lightweight process jail with fine-grained seccomp filters; no dependency on Docker daemon; proven in code runner scenarios.
- **Cons**: Extra setup complexity (requires kernel features, root privileges); less familiarity within current team; no network virtualization—needs iptables rules for egress control.

## Decision (short term)
- Adopt **Docker-based sandboxing** first. It integrates seamlessly with our containerized deployment (Kubernetes or Compose) and leverages existing knowledge.
- Revisit NSJail once we need lower latency or wish to decouple from Docker.

## Policy requirements
- CPU quota per execution (default 1 CPU, configurable per task).
- Memory limit (default 1 GiB).
- Wall-clock timeout (default 120s) with immediate termination.
- Network egress disabled by default; allowlist per task (e.g., API lookups).
- Filesystem mounted read-only except work directory; persist artifacts via explicit copy-out.
- Environment variables filtered to explicit allowlist (no inheritance of secrets).

The following configuration format (`config/security_policy.yaml`) captures these requirements. Executor will enforce the policy before launching containers.
