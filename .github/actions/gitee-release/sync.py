"""独立 Release 同步入口；协调器是正文唯一写者，附件任务仅返回结果。"""

from collections.abc import Mapping
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import threading
import time

from sync_api import SyncAPI, SyncAPIError, valid_ref
from sync_rules import select_assets, parse_index, decide_asset, merge_index


_LOG_LOCK = threading.Lock()


def resolve_config(environment: Mapping[str, str]) -> dict:
    """固定本轮来源、目标和选择；非法显式值不回退默认配置。"""
    repository = environment.get("GITEE_REPOSITORY", "xxf1ow/autolib-release")
    tag = environment.get("GITEE_RELEASE_TAG", "autolib-release")
    packages = environment.get("SYNC_PACKAGES", "")
    github_token, gitee_token = environment.get("GITHUB_TOKEN", ""), environment.get("GITEE_TOKEN", "")
    if (not isinstance(repository, str) or not re.fullmatch(r"[A-Za-z0-9_-]+/[A-Za-z0-9_.-]+", repository)
            or repository.split("/")[-1] in {".", ".."} or not valid_ref("refs/tags/", tag)
            or not isinstance(packages, str) or not github_token or not gitee_token):
        raise ValueError("invalid_sync_configuration")
    if packages.strip() and any(not item.strip() for item in packages.split(",")):
        raise ValueError("empty_package_selection")
    return dict(repository=repository, tag=tag, packages=packages,
                github_token=github_token, gitee_token=gitee_token)


def _log(record, secrets=()):
    encoded = json.dumps(record, ensure_ascii=True)
    for secret in secrets:
        if secret:
            encoded = encoded.replace(json.dumps(secret)[1:-1], "[REDACTED]")
    with _LOG_LOCK:
        print(encoded, flush=True)


def sync_one(api: SyncAPI, release_id: int, source: dict, target_id: int | None,
             temporary_root: Path) -> dict:
    """处理一个附件；先校验来源字节，再删除唯一旧目标，绝不提交正文。"""
    started, name = time.monotonic(), source["name"]
    secrets = (api.github_token, api.gitee_token)
    _log(dict(event="asset_start", name=name), secrets)
    try:
        file = temporary_root / name
        api.download(source, file)
        if target_id is not None:
            api.delete_asset(release_id, target_id)
        uploaded = api.upload_asset(release_id, source, file)
        result = dict(name=name, status="uploaded", entry={
            "sha256": source["digest"][7:].lower(), "gitee_asset_id": uploaded["id"]})
    except SyncAPIError as error:
        result = dict(name=name, status="failed", reason=error.operation, outcome=error.outcome)
    except (ValueError, OSError):
        result = dict(name=name, status="failed", reason="invalid_asset_or_local_file")
    except Exception:
        result = dict(name=name, status="failed", reason="unexpected_asset_failure")
    _log(dict(event="asset_end", name=name, status=result["status"],
              elapsed_seconds=round(time.monotonic() - started, 3),
              **{key: result[key] for key in ("reason", "outcome") if key in result}), secrets)
    return result


def sync_release(config: dict) -> dict:
    """读取一次快照并同步；失败项允许残留旧记录，不回滚已上传的字节。"""
    started = time.monotonic()
    api = SyncAPI(config["github_token"], config["gitee_token"], config["repository"])
    secrets = (config["github_token"], config["gitee_token"])
    try:
        sources = select_assets(api.source_assets(config["tag"]), config["packages"])
        release = api.target_release(config["tag"])
        body = release["body"] if release else ""
        index = parse_index(body)
        targets = api.target_assets(release["id"]) if release else []
        if release is None:
            release = api.create_release(config["tag"])
            body = release["body"]
        results, pending = {}, []
        for source in sources:
            name = source["name"]
            try:
                if decide_asset(source, targets, index.get(name)) == "skip":
                    results[name] = dict(name=name, status="skipped")
                else:
                    target_id = next((asset["id"] for asset in targets if asset["name"] == name), None)
                    pending.append((source, target_id))
            except ValueError:
                results[name] = dict(name=name, status="failed", reason="invalid_or_duplicate_target")
        cpu_count = len(os.sched_getaffinity(0))
        workers = min(cpu_count, len(pending))
        _log(dict(event="concurrency", cpu_count=cpu_count, workers=workers), secrets)
        if pending:
            with tempfile.TemporaryDirectory(prefix="autolib-gitee-sync-") as temporary:
                with ThreadPoolExecutor(max_workers=workers) as pool:
                    futures = [(source["name"], pool.submit(sync_one, api, release["id"], source,
                                                           target_id, Path(temporary)))
                               for source, target_id in pending]
                    for name, future in futures:
                        results[name] = future.result()
        successes = {name: item["entry"] for name, item in results.items() if item["status"] == "uploaded"}
        updated = merge_index(body, successes)
        body_status = "unchanged"
        if updated != body:
            try:
                api.update_body(release, updated)
                body_status = "saved"
            except SyncAPIError as error:
                body_status = error.outcome
        result = {"selected": [source["name"] for source in sources], "body_status": body_status,
                  "cpu_count": cpu_count, "workers": workers, "rate_limited": api.rate_limited,
                  "elapsed_seconds": round(time.monotonic() - started, 3)}
        for status in ("skipped", "uploaded", "failed"):
            result[status] = [source["name"] for source in sources if results[source["name"]]["status"] == status]
        _log(dict(event="summary", **result,
                  counts={key: len(result[key]) for key in ("selected", "skipped", "uploaded", "failed")}), secrets)
        return result
    finally:
        api.close()


def main() -> int:
    """环境输入入口；任何附件或正文失败使独立同步返回非零。"""
    try:
        result = sync_release(resolve_config(os.environ))
        return 1 if result["failed"] or result["body_status"] in {"failed", "unknown"} else 0
    except SyncAPIError as error:
        _log(dict(event="sync_failed", operation=error.operation, outcome=error.outcome, status=error.status))
    except Exception:
        _log(dict(event="sync_failed", outcome="failed", reason="invalid_input_or_runtime"))
    return 1


if __name__ == "__main__":
    sys.exit(main())
