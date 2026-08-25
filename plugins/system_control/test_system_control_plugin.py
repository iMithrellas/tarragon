import subprocess
import unittest
from unittest import mock

import system_control_plugin as plugin


class SystemControlPluginTest(unittest.TestCase):
    def test_empty_query_lists_all_actions(self):
        results = plugin.process("")

        self.assertEqual([item["id"] for item in results], list(plugin.COMMANDS_BY_ID))
        self.assertTrue(all(item["actions"][0]["default"] for item in results))

    def test_query_matches_synonyms(self):
        self.assertEqual([item["id"] for item in plugin.process("shutdown")], ["poweroff"])
        self.assertEqual(
            [item["id"] for item in plugin.process("sleep")],
            ["suspend", "hibernate", "suspend-then-hibernate"],
        )

    @mock.patch.object(plugin.subprocess, "run")
    def test_reboot_uses_fixed_non_blocking_command(self, run):
        run.return_value = subprocess.CompletedProcess([], 0, "", "")

        success, message = plugin.run_command("reboot", "run")

        self.assertTrue(success)
        self.assertEqual(message, "Restart Computer requested")
        run.assert_called_once_with(
            ["systemctl", "--no-block", "reboot"],
            capture_output=True,
            text=True,
            timeout=10,
        )

    @mock.patch.object(plugin.subprocess, "run")
    def test_unknown_command_is_not_executed(self, run):
        success, message = plugin.run_command("anything")

        self.assertFalse(success)
        self.assertEqual(message, "unknown command: anything")
        run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
