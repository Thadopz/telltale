#!/usr/bin/env bash
set -euo pipefail

export PATH=/home/cyq22/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PYTHONUNBUFFERED=1

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
config_file="${repo_root}/manifest/config/config.local.yaml"
eval "$(python3 "${repo_root}/scripts/sregym_openai_env.py" "${config_file}" --section quick_chat_model)"
export SREGYM_MODEL="${SREGYM_MODEL:-openai/deepseek-v4-pro}"

cd /home/cyq22/SREGym

log="/home/cyq22/SREGym/superbiz_sregym_run_$(date +%Y%m%d_%H%M%S)_file_runner.log"

nohup bash -c '
  set -o pipefail
  export PATH=/home/cyq22/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
  export PYTHONUNBUFFERED=1
  cd /home/cyq22/SREGym
  uv run python main.py \
    --agent superbiz-sreagent \
    --model "${SREGYM_MODEL}" \
    --judge-model "${SREGYM_JUDGE_MODEL}" \
    --problem incorrect_image \
    --agent-timeout 900
  rc=$?
  echo "EXIT_CODE:${rc}"
  exit "${rc}"
' > "${log}" 2>&1 < /dev/null &

echo "$!" > /home/cyq22/SREGym/superbiz_sregym_run.pid
echo "${log}"
