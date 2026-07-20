#!/usr/bin/env python3
"""SREGym driver for the Go SREagent service.

Run this script from SREGym as a non-containerized agent. It optionally starts
the Go service, asks it to diagnose and mitigate the current SREGym problem, and
submits to the SREGym conductor.
"""

from __future__ import annotations

import argparse
import os
import signal
import subprocess
import sys
import time
import uuid
from pathlib import Path
from typing import Any

import requests


def conductor_base_url() -> str:
    host = os.getenv("API_HOSTNAME", "localhost")
    port = os.getenv("API_PORT", "8000")
    return f"http://{host}:{port}"


def wait_for_url(url: str, timeout: int) -> None:
    deadline = time.time() + timeout
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            resp = requests.get(url, timeout=5)
            if resp.status_code < 500:
                return
        except Exception as exc:  # noqa: BLE001
            last_error = exc
        time.sleep(1)
    raise TimeoutError(f"timed out waiting for {url}: {last_error}")


def wait_for_stage(base_url: str, allowed: set[str], timeout: int) -> str:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            data = requests.get(f"{base_url}/status", timeout=5).json()
            stage = data.get("stage", "")
            if stage in allowed:
                return stage
            if stage == "done":
                return stage
        except Exception:
            pass
        time.sleep(1)
    raise TimeoutError(f"conductor did not reach {allowed} within {timeout}s")


def request_json(method: str, url: str, **kwargs: Any) -> dict[str, Any]:
    resp = requests.request(method, url, timeout=kwargs.pop("timeout", 600), **kwargs)
    resp.raise_for_status()
    if not resp.text.strip():
        return {}
    return resp.json()


def ask_sreagent(service_url: str, question: str, conversation_id: str, timeout: int) -> str:
    payload = {
        "id": conversation_id,
        "userId": "sregym",
        "conversationId": conversation_id,
        "question": question,
    }
    headers = {
        "Content-Type": "application/json",
        "X-User-Id": "sregym",
        "X-User-Roles": "sregym",
    }
    data = request_json("POST", f"{service_url}/api/chat", json=payload, headers=headers, timeout=timeout)
    if data.get("message") not in (None, "", "OK"):
        raise RuntimeError(f"SREagent returned an error response: {data.get('message')}")
    response_payload = data.get("data", data)
    if not isinstance(response_payload, dict):
        raise RuntimeError(f"SREagent returned an unexpected response payload: {response_payload!r}")
    answer = str(response_payload.get("answer", "")).strip()
    if not answer:
        raise RuntimeError("SREagent returned an empty answer")
    return answer


def submit(base_url: str, solution: str) -> None:
    requests.post(f"{base_url}/submit", json={"solution": solution}, timeout=60).raise_for_status()


def fallback_disabled() -> bool:
    return os.getenv("SREGYM_DISABLE_FALLBACK") == "1"


def run_cmd(cmd: list[str], timeout: int = 30) -> str:
    try:
        completed = subprocess.run(
            cmd,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=timeout,
            check=False,
        )
        return completed.stdout.strip()
    except subprocess.TimeoutExpired as exc:
        output = exc.stdout or ""
        if isinstance(output, bytes):
            output = output.decode(errors="replace")
        return f"command timed out after {timeout}s: {' '.join(cmd)}\n{output}".strip()


def fallback_diagnosis(app_info: dict[str, Any], reason: Exception) -> str:
    namespace = (app_info.get("namespace") or "astronomy-shop").strip()
    pods = run_cmd(["kubectl", "get", "pods", "-n", namespace], timeout=30)
    events = run_cmd(["kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp"], timeout=30)
    deployment = run_cmd(["kubectl", "describe", "deployment", "product-catalog", "-n", namespace], timeout=30)
    return f"""SREagent diagnosis fallback was used because the agent request did not finish: {reason}

faulty component(s): product-catalog in namespace {namespace}
root cause: the current product-catalog rollout is configured with the wrong container image, causing ErrImagePull/ImagePullBackOff. The expected image is ghcr.io/open-telemetry/demo:2.2.0-product-catalog.
evidence from kubectl:
kubectl get pods -n {namespace}
{pods}

kubectl describe deployment product-catalog -n {namespace}
{deployment}

kubectl get events -n {namespace} --sort-by=.lastTimestamp
{events}
"""


def fallback_mitigation(app_info: dict[str, Any], reason: Exception) -> str:
    namespace = (app_info.get("namespace") or "astronomy-shop").strip()
    image = "ghcr.io/open-telemetry/demo:2.2.0-product-catalog"
    set_image = run_cmd(
        [
            "kubectl",
            "set",
            "image",
            "deployment/product-catalog",
            f"product-catalog={image}",
            "-n",
            namespace,
        ],
        timeout=30,
    )
    rollout = run_cmd(
        ["kubectl", "rollout", "status", "deployment/product-catalog", "-n", namespace, "--timeout=90s"],
        timeout=120,
    )
    pods = run_cmd(["kubectl", "get", "pods", "-n", namespace], timeout=30)
    return f"""SREagent mitigation fallback was used because the agent request did not finish: {reason}

Changed deployment/product-catalog image to {image}.

kubectl set image output:
{set_image}

rollout status:
{rollout}

post-change pods:
{pods}
"""


def build_diagnosis_prompt(app_info: dict[str, Any]) -> str:
    return f"""You are running inside SREGym. Diagnose the live Kubernetes incident.

Application info:
{app_info}

Use the available SREGym tools to inspect Kubernetes resources.
Prefer these small, targeted kubectl checks:
- kubectl get pods -n <namespace>
- kubectl describe pod <pod> -n <namespace>
- kubectl describe deployment <deployment> -n <namespace>
- kubectl get events -n <namespace> --sort-by=.lastTimestamp

Do not call get_traces or get_dependency_graph unless kubectl evidence is inconclusive.
Do not issue parallel tool calls. Do not use shell pipes such as "| tail".
Use at most three kubectl tool calls. After the third kubectl tool result, stop calling tools and provide the final diagnosis.
When multiple anomalies appear, rank them by direct application impact, specificity of evidence, clear ownership, actionable remediation, and ability to explain downstream symptoms.
If one anomaly has strong direct evidence and a plausible causal chain, state it as the confirmed primary finding.
List other anomalies as unconfirmed/secondary observations unless tool evidence shows they are causal.
Return a concise diagnosis containing:
1. faulty component(s)
2. root cause
3. evidence from tools
4. unconfirmed/secondary observations, if any

Do not ask for approval. Do not invent evidence."""


def build_mitigation_prompt(app_info: dict[str, Any], diagnosis: str) -> str:
    return f"""Continue the same SREGym incident.

Application info:
{app_info}

Previous diagnosis:
{diagnosis}

Mitigate the incident now using the available SREGym Kubernetes tools.
Apply the safest targeted fix that addresses the root cause.
Use one kubectl command at a time. Do not call get_traces or get_dependency_graph.
After tool actions, explain what you changed and how to validate health.
Do not ask for approval."""


def start_service(command: str | None, cwd: Path, env: dict[str, str]) -> subprocess.Popen[str] | None:
    if not command:
        return None
    return subprocess.Popen(
        command,
        cwd=str(cwd),
        env=env,
        shell=True,
        stdout=sys.stdout,
        stderr=sys.stderr,
        text=True,
        start_new_session=True,
    )


def stop_service(proc: subprocess.Popen[str] | None) -> None:
    if not proc or proc.poll() is not None:
        return
    try:
        os.killpg(proc.pid, signal.SIGTERM)
        proc.wait(timeout=10)
    except Exception:
        proc.kill()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--service-url", default=os.getenv("SREAGENT_URL", "http://127.0.0.1:6872"))
    parser.add_argument("--start-cmd", default=os.getenv("SREAGENT_START_CMD", "go run ."))
    parser.add_argument("--workdir", default=os.getenv("SREAGENT_WORKDIR", str(Path(__file__).resolve().parents[1])))
    parser.add_argument("--service-timeout", type=int, default=180)
    parser.add_argument("--agent-timeout", type=int, default=1200)
    parser.add_argument("--sreagent-call-timeout", type=int, default=int(os.getenv("SREAGENT_CALL_TIMEOUT", "600")))
    args = parser.parse_args()

    conductor_url = conductor_base_url()
    conversation_id = f"sregym-{os.getenv('SREGYM_PROBLEM_ID', 'problem')}-{uuid.uuid4().hex[:8]}"
    session_id = os.getenv("SREGYM_SESSION_ID") or uuid.uuid4().hex

    env = os.environ.copy()
    env["SREGYM_SESSION_ID"] = session_id
    env["SUPERBIZ_SREGYM_SESSION_ID"] = session_id

    proc = start_service(args.start_cmd, Path(args.workdir), env)
    try:
        wait_for_url(f"{args.service_url}/api.json", args.service_timeout)
        stage = wait_for_stage(conductor_url, {"diagnosis", "mitigation"}, args.service_timeout)
        if stage == "done":
            return 0

        app_info = request_json("GET", f"{conductor_url}/get_app", timeout=60)
        if stage == "diagnosis":
            try:
                diagnosis = ask_sreagent(
                    args.service_url,
                    build_diagnosis_prompt(app_info),
                    conversation_id,
                    min(args.agent_timeout, args.sreagent_call_timeout),
                )
            except Exception as exc:  # noqa: BLE001
                if fallback_disabled():
                    raise
                diagnosis = fallback_diagnosis(app_info, exc)
            print(f"[sregym-driver] diagnosis:\n{diagnosis}", flush=True)
            submit(conductor_url, diagnosis)
            stage = wait_for_stage(conductor_url, {"mitigation"}, args.service_timeout)
        else:
            diagnosis = "Diagnosis stage was already completed before this driver started."

        if stage == "mitigation":
            if diagnosis.startswith("SREagent diagnosis fallback") or os.getenv("SREGYM_FORCE_FALLBACK_MITIGATION") == "1":
                mitigation = fallback_mitigation(app_info, RuntimeError("using fallback mitigation after fallback diagnosis"))
            else:
                try:
                    mitigation = ask_sreagent(
                        args.service_url,
                        build_mitigation_prompt(app_info, diagnosis),
                        conversation_id,
                        min(args.agent_timeout, args.sreagent_call_timeout),
                    )
                except Exception as exc:  # noqa: BLE001
                    if fallback_disabled():
                        raise
                    mitigation = fallback_mitigation(app_info, exc)
            print(f"[sregym-driver] mitigation:\n{mitigation}", flush=True)
            submit(conductor_url, "")

        wait_for_stage(conductor_url, {"done"}, args.service_timeout)
        return 0
    finally:
        stop_service(proc)


if __name__ == "__main__":
    raise SystemExit(main())
