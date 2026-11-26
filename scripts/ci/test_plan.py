import json
import unittest
from pathlib import Path

from plan import plan
from release_plan import DEFAULT_WORKFLOW, dry_run_cells


class CIPlanTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.areas = json.loads(Path(".github/ci/areas.json").read_text())
        cls.releases = json.loads(Path(".github/ci/releases.json").read_text())

    def cells(self, paths=(), labels=(), event="pull_request", workflow=None):
        workflows = (workflow,) if workflow else self.areas["workflows"]
        return [
            cell
            for workflow in workflows
            for cells in plan(self.areas, event, labels, paths, workflow)[0].values()
            for cell in cells
        ]

    def test_ruby_change_only_runs_ruby(self):
        for paths, labels in [
            (["pkg/ext-ruby/lib/ext_io/host.rb"], []),
            ([], ["tool:ruby"]),
        ]:
            with self.subTest(paths=paths, labels=labels):
                cells = self.cells(paths, labels)
                self.assertTrue(any(cell["name"].startswith("ext-ruby/") for cell in cells))
                hosts = [cell for cell in cells if cell["name"].startswith("host-cases/")]
                self.assertTrue(hosts)
                for cell in hosts:
                    self.assertEqual(cell["vars"]["TARGETS"], "ruby")
                    self.assertEqual(set(cell["setup"]), {"go", "go_cache", "ruby"})
                self.assertTrue(all(cell["name"].startswith(("ext-ruby/", "host-cases/hosts")) for cell in cells))

    def test_other_package_changes_stay_scoped(self):
        cases = {
            "pkg/ext-php/src/Host.php": ("host-cases/hosts [php]", "ext-php/php"),
            "pkg/ext-lua/ext_io/host.lua": ("host-cases/hosts [lua]", "ext-lua/lua", "ext-lua/rock"),
            "pkg/ext-dotnet/ExtIO/Plugin.cs": ("host-cases/hosts [csharp]", "ext-dotnet/guest"),
            "pkg/ext-python/ext_io/plugin.py": ("host-cases/hosts [python]", "plugin-sdk/language-sdks [python]"),
            "pkg/ext-ts/test.mjs": ("plugin-sdk/language-sdks [javascript]",),
            "pkg/ext-web/test.mjs": ("web-js/extension-library",),
        }
        for path, expected in cases.items():
            with self.subTest(path=path):
                jobs = {cell["name"].partition(" (")[0] for cell in self.cells([path])}
                self.assertEqual(jobs, set(expected))

    def test_language_sdk_checks_belong_to_runtime(self):
        script = "scripts/ci/jobs/plugin-sdk/language-sdks.sh"
        for paths, labels, target in [
            (["pkg/ext-python/ext_io/plugin.py"], [], "python"),
            (["pkg/ext-ts/test.mjs"], [], "javascript"),
            ([], ["tool:python"], "python"),
            ([], ["tool:javascript"], "javascript"),
        ]:
            with self.subTest(paths=paths, labels=labels):
                runtime = self.cells(paths, labels, workflow="runtime")
                self.assertEqual([cell["vars"]["TARGETS"] for cell in runtime if cell["script"] == script], [target])
                core = self.cells(paths, labels, workflow="core")
                self.assertFalse(any(cell["script"] == script for cell in core))

    def test_multiple_hosts_share_each_runner(self):
        cells = self.cells(["pkg/ext-ruby/lib/ext_io.rb", "pkg/ext-python/ext_io/plugin.py"])
        hosts = [cell for cell in cells if cell["name"].startswith("host-cases/")]
        self.assertTrue(hosts)
        for cell in hosts:
            self.assertEqual(cell["vars"]["TARGETS"], "python ruby")
            self.assertEqual(set(cell["setup"]), {"go", "go_cache", "python", "ruby"})

    def test_shared_changes_and_full_runs_keep_full_host_suite(self):
        for paths, labels, event in [
            (["lib/plugin/testdata/v1/host-cases.json"], [], "pull_request"),
            ([], ["ci:full"], "pull_request"),
            (["pkg/ext-ruby/lib/ext_io/host.rb"], [], "push"),
        ]:
            with self.subTest(paths=paths, labels=labels, event=event):
                cells = self.cells(paths, labels, event)
                self.assertTrue(any(cell["name"].startswith("host-cases/go-purego") for cell in cells))
                hosts = [cell for cell in cells if cell["name"].startswith("host-cases/hosts")]
                self.assertTrue(hosts)
                for cell in hosts:
                    self.assertNotIn("TARGETS", cell["vars"])
                    self.assertTrue({"python", "ruby", "lua", "php", "dotnet"} <= cell["setup"].keys())

    def test_platform_specific_changes_use_their_runner(self):
        path = "lib/system/web/windows/Ext.Web.Windows.csproj"
        cells = self.cells([path])
        self.assertTrue(cells)
        self.assertTrue(all(cell["runner"].startswith("windows") for cell in cells))
        self.assertEqual(self.cells([path], ["platform:linux"]), [])
        self.assertEqual(self.cells(["docs/ci.md"]), [])

    def test_release_labels_route_to_one_owner(self):
        packages = self.releases["packages"]
        labels = [f"release:{name}" for name in packages]
        for workflow in self.areas["workflows"]:
            with self.subTest(workflow=workflow):
                self.assertEqual(dry_run_cells(self.releases, workflow, []), ([], []))
                cells, chosen = dry_run_cells(self.releases, workflow, labels)
                expected = {name for name, package in packages.items()
                            if package.get("workflow", DEFAULT_WORKFLOW) == workflow}
                self.assertEqual(set(chosen), expected)
                self.assertEqual(len(cells), sum(len(packages[name]["build"]) or 1 for name in chosen))
        with self.assertRaisesRegex(SystemExit, "unknown release packages"):
            dry_run_cells(self.releases, "core", ["release:unknown"])
