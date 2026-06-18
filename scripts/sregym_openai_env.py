#!/usr/bin/env python3
"""Emit shell exports for SREGym judge OpenAI-compatible credentials.

The source of truth is manifest/config/config.local.yaml. This helper keeps the
secret out of runner scripts while still making LiteLLM/OpenAI clients see the
environment variables they expect.
"""

from __future__ import annotations

import argparse
import shlex
from pathlib import Path


DEFAULT_SECTION = "quick_chat_model"


def parse_scalar(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
        return value[1:-1]
    return value


def parse_simple_yaml(path: Path) -> dict[str, dict[str, str]]:
    data: dict[str, dict[str, str]] = {}
    section: str | None = None

    for raw in path.read_text(encoding="utf-8").splitlines():
        stripped = raw.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if raw[:1].isspace():
            if section and ":" in stripped:
                key, value = stripped.split(":", 1)
                data.setdefault(section, {})[key.strip()] = parse_scalar(value)
            continue
        if ":" in stripped:
            key, value = stripped.split(":", 1)
            section = key.strip()
            data.setdefault(section, {})
            if value.strip():
                data[section]["_value"] = parse_scalar(value)

    return data


def model_id(model: str, prefix: str) -> str:
    return model if "/" in model else f"{prefix}{model}"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("config", type=Path)
    parser.add_argument("--section", default=DEFAULT_SECTION)
    parser.add_argument("--model-prefix", default="openai/")
    parser.add_argument("--redact", action="store_true", help="Print placeholders instead of secret values.")
    args = parser.parse_args()

    section = parse_simple_yaml(args.config).get(args.section)
    if not section:
        raise SystemExit(f"missing config section: {args.section}")

    api_key = section.get("api_key", "")
    base_url = section.get("base_url", "")
    model = section.get("model", "")
    missing = [name for name, value in [("api_key", api_key), ("base_url", base_url), ("model", model)] if not value]
    if missing:
        raise SystemExit(f"missing {args.section} fields: {', '.join(missing)}")

    env = {
        "OPENAI_API_KEY": "***" if args.redact else api_key,
        "OPENAI_API_BASE": "***" if args.redact else base_url,
        "OPENAI_BASE_URL": "***" if args.redact else base_url,
        "SREGYM_JUDGE_MODEL": model_id(model, args.model_prefix),
    }
    for key, value in env.items():
        print(f"export {key}={shlex.quote(value)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
