"""同步选择和正文索引；不请求网络，不将同步记录视为内容认证。"""

import json
import re
from urllib.parse import urlsplit


START = "<!-- autolib-gitee-sync:start -->"
END = "<!-- autolib-gitee-sync:end -->"
IMPORTERS = frozenset(("autolib-importer-windows-x86_64.exe.tar.xz",
                       "autolib-importer-linux-x86_64.tar.xz",
                       "autolib-importer-linux-aarch64.tar.xz"))
PACKAGE = re.compile(r"(.+)-(?:windows-x86_64|linux-x86_64|linux-aarch64|any-any)\.tar\.zst")
DIGEST = re.compile(r"[0-9a-fA-F]{64}")


def valid_id(value):
    return type(value) is int and value > 0


def valid_https(value):
    if not isinstance(value, str) or any(c.isspace() or ord(c) < 32 for c in value):
        return False
    try:
        parsed = urlsplit(value)
        return parsed.scheme == "https" and bool(parsed.hostname) and not parsed.username and not parsed.password
    except ValueError:
        return False


def _safe_name(name):
    return (isinstance(name, str) and bool(name) and name not in (".", "..")
            and not any(c in '/\\:' or ord(c) < 32 or ord(c) == 127 for c in name))


def select_assets(assets: list[dict], packages: str) -> list[dict]:
    """按完整包名选择并校验来源；任一指定项未匹配时拒绝整个输入。"""
    if not isinstance(assets, list) or not isinstance(packages, str):
        raise ValueError("invalid_source_selection")
    requested = set()
    if packages.strip():
        requested = {item.strip() for item in packages.split(",")}
        if "" in requested:
            raise ValueError("empty_package_selection")
    names, identities, matched, selected = set(), set(), set(), []
    for asset in assets:
        if not isinstance(asset, dict) or not valid_id(asset.get("id")) or not _safe_name(asset.get("name")):
            raise ValueError("invalid_source_identity")
        name = asset["name"]
        if name in names or asset["id"] in identities:
            raise ValueError("duplicate_source_identity")
        names.add(name)
        identities.add(asset["id"])
        match = PACKAGE.fullmatch(name)
        package = "autolib-importer" if name in IMPORTERS else match[1] if match else None
        if requested and package not in requested:
            continue
        digest = asset.get("digest")
        expected_url = f'https://api.github.com/repos/xxf1ow/xxautolib/releases/assets/{asset["id"]}'
        if (type(asset.get("size")) is not int or asset["size"] < 0 or asset.get("state") != "uploaded"
                or not isinstance(digest, str) or not digest.startswith("sha256:")
                or not DIGEST.fullmatch(digest[7:]) or asset.get("url") != expected_url):
            raise ValueError("invalid_source_asset")
        matched.add(package)
        selected.append(asset)
    if requested - matched:
        raise ValueError("unmatched_package_selection")
    return selected


def _index_span(body):
    if not isinstance(body, str):
        raise ValueError("invalid_release_body")
    if START not in body and END not in body:
        return None
    if body.count(START) != 1 or body.count(END) != 1:
        raise ValueError("invalid_index_markers")
    start, end = body.index(START), body.index(END)
    if start >= end:
        raise ValueError("invalid_index_markers")
    for offset, marker in ((start, START), (end, END)):
        tail = body[offset + len(marker):]
        if (offset > 0 and body[offset - 1] != "\n") or (tail and not tail.startswith(("\n", "\r\n"))):
            raise ValueError("invalid_index_marker_line")
    return start, end + len(END)


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate_index_key")
        result[key] = value
    return result


def _reject_constant(value):
    raise ValueError("invalid_json_constant")


def _validate_files(files):
    if not isinstance(files, dict):
        raise ValueError("invalid_index_files")
    for name, entry in files.items():
        if (not isinstance(name, str) or not name or not isinstance(entry, dict)
                or set(entry) != {"sha256", "gitee_asset_id"}
                or not isinstance(entry["sha256"], str) or not DIGEST.fullmatch(entry["sha256"])
                or not valid_id(entry["gitee_asset_id"])):
            raise ValueError("invalid_index_entry")


def parse_index(body: str) -> dict[str, dict]:
    """返回记录；无区块为空，保留语法有歧义时拒绝而不尝试修复。"""
    span = _index_span(body)
    if span is None:
        return {}
    content = body[span[0] + len(START):span[1] - len(END)]
    block = re.fullmatch(r"\s*```json\r?\n(.*?)\r?\n```\s*", content, re.DOTALL)
    if not block:
        raise ValueError("invalid_index_code_block")
    document = json.loads(block[1], object_pairs_hook=_unique_object, parse_constant=_reject_constant)
    if not isinstance(document, dict) or set(document) != {"files"}:
        raise ValueError("invalid_index_document")
    _validate_files(document["files"])
    return document["files"]


def validate_target(asset):
    """校验所选目标附件的身份字段；调用者决定将失败归到哪个名称。"""
    if (not isinstance(asset, dict) or not valid_id(asset.get("id"))
            or not isinstance(asset.get("name"), str) or not asset["name"]
            or type(asset.get("size")) is not int or asset["size"] < 0
            or not valid_https(asset.get("browser_download_url"))):
        raise ValueError("invalid_target_asset")


def decide_asset(source: dict, targets: list[dict], entry: dict | None) -> str:
    """仅完整一致才跳过；重复同名或坏目标拒绝，不猜测删除对象。"""
    matches = [asset for asset in targets if asset.get("name") == source["name"]]
    if len(matches) > 1:
        raise ValueError("duplicate_target_asset")
    if not matches:
        return "sync"
    target = matches[0]
    validate_target(target)
    if (entry is not None and entry["sha256"].lower() == source["digest"][7:].lower()
            and entry["gitee_asset_id"] == target["id"] and target["size"] == source["size"]):
        return "skip"
    return "sync"


def merge_index(body: str, successes: dict[str, dict]) -> str:
    """只覆盖成功项；未选择和失败旧记录保留，区块外文本不变。"""
    files = parse_index(body)
    _validate_files(successes)
    updated = dict(files)
    updated.update({name: dict(entry, sha256=entry["sha256"].lower()) for name, entry in successes.items()})
    if updated == files:
        return body
    encoded = json.dumps({"files": updated}, ensure_ascii=False, sort_keys=True, indent=2)
    block = START + "\n```json\n" + encoded + "\n```\n" + END
    span = _index_span(body)
    if span is None:
        return body + ("\n" if body and not body.endswith("\n") else "") + block
    return body[:span[0]] + block + body[span[1]:]
