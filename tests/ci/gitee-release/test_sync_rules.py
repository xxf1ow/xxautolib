"""选择和索引规则：错误元数据不能变成附件跳过或猜测删除。"""

import importlib
import json
from pathlib import Path
import sys
import unittest


ACTION = Path(__file__).resolve().parents[3] / ".github/actions/gitee-release"
START = "<!-- autolib-gitee-sync:start -->"
END = "<!-- autolib-gitee-sync:end -->"


def source(name="lz4-linux-x86_64.tar.zst", identity=1):
    return {"id": identity, "name": name, "size": 3, "state": "uploaded",
            "digest": "sha256:" + "a" * 64,
            "url": f"https://api.github.com/repos/xxf1ow/xxautolib/releases/assets/{identity}"}


def body_for(files):
    return START + "\n```json\n" + json.dumps({"files": files}) + "\n```\n" + END


class RulesTests(unittest.TestCase):
    def setUp(self):
        self.assertTrue((ACTION / "sync_rules.py").is_file(), "sync rules not implemented")
        sys.path.insert(0, str(ACTION))
        self.addCleanup(sys.path.remove, str(ACTION))
        self.rules = importlib.import_module("sync_rules")
        self.source = source()
        self.target = {"id": 7, "name": self.source["name"], "size": 3,
                       "browser_download_url": "https://gitee.com/fixture/download"}
        self.entry = {"sha256": "a" * 64, "gitee_asset_id": 7}

    def test_selection_exact_package_and_unmatched(self):
        names = ["queue-any-any.tar.zst", "lz4-linux-x86_64.tar.zst",
                 "lz4-linux-aarch64.tar.zst", "lz4-extra-linux-x86_64.tar.zst",
                 "flann-windows-x86_64.tar.zst", "my-lib-linux-x86_64.tar.zst"]
        assets = [source(name, i + 1) for i, name in enumerate(names)]
        self.assertEqual([a["name"] for a in self.rules.select_assets(assets, " lz4,lz4 ")], names[1:3])
        self.assertEqual(self.rules.select_assets(assets, "my-lib"), [assets[-1]])
        self.assertEqual(self.rules.select_assets(assets, " \t"), assets)
        for packages in ("lz4,missing", "lz4,", ",", "lz", "lz4,,queue"):
            with self.subTest(packages=packages), self.assertRaises(ValueError):
                self.rules.select_assets(assets, packages)

    def test_importer_requires_explicit_selection(self):
        names = ["autolib-importer-windows-x86_64.exe.tar.xz",
                 "autolib-importer-linux-x86_64.tar.xz", "autolib-importer-linux-aarch64.tar.xz"]
        assets = [self.source] + [source(name, i + 2) for i, name in enumerate(names)]
        self.assertEqual(self.rules.select_assets(assets, "lz4"), [self.source])
        self.assertEqual([a["name"] for a in self.rules.select_assets(assets, "autolib-importer")], names)

    def test_selected_source_identity_and_digest_are_required(self):
        invalid = [{"name": name} for name in ("../bad", "a/b", "a\\b", ".", "..", "x\nname", "x\x00")]
        invalid += [{"id": True}, {"id": 0}, {"size": True}, {"size": -1},
                    {"state": "new"}, {"digest": None}, {"digest": "sha256:bad"},
                    {"url": "http://api.github.com/asset"}]
        for fields in invalid:
            with self.subTest(fields=fields), self.assertRaises(ValueError):
                self.rules.select_assets([dict(self.source, **fields)], "")
        with self.assertRaises(ValueError):
            self.rules.select_assets([self.source, dict(self.source, id=2)], "")

    def test_skip_requires_digest_id_and_size(self):
        decide = self.rules.decide_asset
        self.assertEqual(decide(self.source, [self.target], self.entry), "skip")
        self.assertEqual(decide(self.source, [self.target], dict(self.entry, sha256="A" * 64)), "skip")
        for targets, entry in (([], self.entry), ([dict(self.target, id=8)], self.entry),
                               ([dict(self.target, size=4)], self.entry), ([self.target], None),
                               ([self.target], dict(self.entry, sha256="b" * 64))):
            with self.subTest(targets=targets, entry=entry):
                self.assertEqual(decide(self.source, targets, entry), "sync")

    def test_duplicate_or_malformed_target_is_not_deleted_by_guess(self):
        for targets in ([self.target, dict(self.target, id=8)], [dict(self.target, id=True)],
                        [dict(self.target, size=None)], [dict(self.target, browser_download_url="http://bad")]):
            with self.subTest(targets=targets), self.assertRaises(ValueError):
                self.rules.decide_asset(self.source, targets, self.entry)
        self.assertEqual(self.rules.decide_asset(self.source, [dict(self.target, name="unrelated")], self.entry), "sync")

    def test_index_grammar_and_preserved_description(self):
        files = {self.source["name"]: self.entry, "unknown.bin": dict(self.entry, gitee_asset_id=99)}
        prefix, suffix = "说明\r\n\r\n", "\r\n结尾\r\n"
        body = prefix + body_for(files).replace("\n", "\r\n") + suffix
        self.assertEqual(self.rules.parse_index(body), files)
        self.assertEqual(self.rules.merge_index(body, {}), body)
        new_entry = {"sha256": "b" * 64, "gitee_asset_id": 8}
        merged = self.rules.merge_index(body, {self.source["name"]: new_entry})
        self.assertTrue(merged.startswith(prefix))
        self.assertTrue(merged.endswith(suffix))
        self.assertEqual(self.rules.parse_index(merged), {self.source["name"]: new_entry, "unknown.bin": files["unknown.bin"]})

    def test_malformed_markers_and_multiple_blocks_are_rejected(self):
        good = body_for({})
        invalid = [START, END, END + "\n" + START, good + "\n" + good,
                   good.replace(START, " " + START), good.replace(END, "> " + END),
                   good.replace(END, START + "\n" + END),
                   good.replace("```json", "extra\n```json"),
                   good.replace("\n" + END, "\n```json\n{}\n```\n" + END),
                   good.replace("```json", "```"), good.replace("\n```\n", "\n")]
        for body in invalid:
            with self.subTest(body=body), self.assertRaises(ValueError):
                self.rules.parse_index(body)

    def test_strict_json_structure_and_duplicate_keys(self):
        raw_values = ['{"files":{},"files":{}}', '{"files":{},"extra":1}', '[]',
                      '{"files":[]}', '{"files":{"x":{"sha256":"' + "a" * 64 + '","sha256":"' + "b" * 64 + '","gitee_asset_id":1}}}']
        for item in (dict(self.entry, gitee_asset_id=True), dict(self.entry, sha256=123),
                     dict(self.entry, sha256="bad"), dict(self.entry, extra=0),
                     dict(self.entry, gitee_asset_id=0), dict(self.entry, sha256=float("nan"))):
            raw_values.append(json.dumps({"files": {"x": item}}))
        for raw in raw_values:
            body = START + "\n```json\n" + raw + "\n```\n" + END
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                self.rules.parse_index(body)

    def test_escaped_markers_are_description_not_an_index(self):
        escaped = "&lt;!-- autolib-gitee-sync:start --&gt;\n普通说明"
        self.assertEqual(self.rules.parse_index(escaped), {})
        merged = self.rules.merge_index(escaped, {"file.bin": self.entry})
        self.assertTrue(merged.startswith(escaped + "\n"))
        self.assertEqual(self.rules.parse_index(merged), {"file.bin": self.entry})
        self.assertEqual(self.rules.parse_index("```text\n" + body_for({}) + "\n```"), {})

    def test_merge_keeps_failed_and_unselected_records(self):
        old = {"failed.bin": self.entry, "unselected.bin": dict(self.entry, sha256="A" * 64)}
        merged = self.rules.merge_index(body_for(old), {"success.bin": dict(self.entry, gitee_asset_id=10)})
        result = self.rules.parse_index(merged)
        self.assertEqual(result["failed.bin"], old["failed.bin"])
        self.assertEqual(result["unselected.bin"], old["unselected.bin"])
        self.assertEqual(result["success.bin"]["gitee_asset_id"], 10)


if __name__ == "__main__":
    unittest.main()
