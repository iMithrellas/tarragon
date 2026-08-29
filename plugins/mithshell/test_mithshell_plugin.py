import subprocess
import unittest
from unittest import mock

import mithshell_plugin as plugin


class MithshellPluginTest(unittest.TestCase):
    def test_empty_query_lists_supported_actions(self):
        result_ids = [item["id"] for item in plugin.process("")]

        self.assertIn("weather", result_ids)
        self.assertIn("inhibit", result_ids)
        self.assertNotIn("close", result_ids)
        self.assertNotIn("unlock", result_ids)
        self.assertNotIn("status", result_ids)

    def test_notification_query_finds_inhibition_actions(self):
        results = plugin.process("notifications")
        self.assertEqual([item["id"] for item in results], ["inhibit", "inhibit-1h"])
        self.assertTrue(all(item["actions"][0]["type"] == "keep_open" for item in results))

    def test_launch_actions_keep_default_dismiss_behavior(self):
        results = plugin.process("weather")

        self.assertEqual(results[0]["id"], "weather")
        self.assertNotIn("type", results[0]["actions"][0])

    @mock.patch.object(plugin.subprocess, "run")
    def test_weather_uses_fixed_cli_command(self, run):
        run.return_value = subprocess.CompletedProcess([], 0, "", "")

        success, message = plugin.run_command("weather")

        self.assertTrue(success)
        self.assertEqual(message, "Open Weather requested")
        run.assert_called_once_with(
            ["mithshell", "weather"],
            capture_output=True,
            text=True,
            timeout=10,
        )


if __name__ == "__main__":
    unittest.main()
