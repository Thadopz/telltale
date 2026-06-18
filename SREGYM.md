# Running SREagent On SREGym

SREGym evaluates an agent against live Kubernetes incidents. This project needs
SREGym MCP tools enabled so the Eino agent can inspect and change the benchmark
cluster.

## 1. Prepare SREGym

On a Linux host with Docker, kubectl, Helm, uv, and kind or a self-managed
Kubernetes cluster:

```bash
git clone --recurse-submodules https://github.com/SREGym/SREGym
cd SREGym
uv sync
bash kind/setup_kind_cluster.sh x86
```

Kind does not support every SREGym problem. Use SREGym's recommended VM or
bare-metal Kubernetes setup for the full suite.

## 2. Configure This Service

In `manifest/config/config.yaml`, set your model provider, then enable SREGym:

```yaml
tools:
  sregym:
    enabled: true
    required: true

permissions:
  default_role: "sregym"
```

The tool URLs default to SREGym's runtime environment:

```text
http://$API_HOSTNAME:$MCP_SERVER_PORT/kubectl/sse
http://$API_HOSTNAME:$MCP_SERVER_PORT/prometheus/sse
http://$API_HOSTNAME:$MCP_SERVER_PORT/jaeger/sse
```

Override them with `SREGYM_KUBECTL_MCP_URL`,
`SREGYM_PROMETHEUS_MCP_URL`, or `SREGYM_JAEGER_MCP_URL` if needed.

`submit_mcp` is not registered by default because `scripts/sregym_driver.py`
handles stage submissions. Set `tools.sregym.register_submit: true` only if you
want the model itself to call the submit MCP tool.

## 3. Register SREagent In SREGym

Add this entry to SREGym's `agents.yaml`, replacing the path with this repo's
absolute path on the Linux host:

```yaml
  - name: superbiz-sreagent
    kickoff_command: python scripts/sregym_driver.py
    kickoff_workdir: /abs/path/to/SuperBizAgent-release-2026-03-22/sreAgent
    kickoff_env:
      SREAGENT_START_CMD: "go run ."
      SREAGENT_URL: "http://127.0.0.1:6872"
    install_script: null
    agent_version: null
    container_isolation: false
```

`container_isolation: false` is intentional. The driver starts the local Go
service from this repository. If you want container isolation, package this
project into SREGym's agent image first.

## 4. Run A Problem

From the SREGym repo:

```bash
python main.py --agent superbiz-sreagent --model gpt-5 --problem-filter incorrect_image
```

If your SREGym version does not expose `--problem-filter`, run the default
selection or use its current problem-selection flag from `python main.py --help`.

## Runtime Flow

1. SREGym injects a fault and starts the conductor API plus MCP servers.
2. `scripts/sregym_driver.py` starts `go run .`.
3. The Go agent registers SREGym MCP tools and diagnoses the issue.
4. The driver submits the diagnosis to `/submit`.
5. The agent applies mitigation through kubectl MCP tools.
6. The driver submits an empty mitigation solution so SREGym validates health.

## Important Limitations

- The benchmark should run on Linux. Windows is fine for development, but local
  kind/SREGym cluster setup is Linux-oriented.
- SREGym will only be meaningful if the configured model can reliably use tools.
- Keep `tools.sregym.enabled` off outside benchmark runs because it exposes
  Kubernetes-changing tools to the chat agent.
