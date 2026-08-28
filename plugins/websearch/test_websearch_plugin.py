import unittest
from unittest import mock

import websearch_plugin as plugin


class WebsearchPluginTest(unittest.TestCase):
    def test_uses_default_symbol_when_env_is_absent(self):
        with mock.patch.dict(plugin.os.environ, {}, clear=True):
            results = plugin.process("@yt ambient music")

        self.assertEqual(len(results), 1)
        self.assertEqual(
            results[0]["id"],
            "https://www.youtube.com/results?search_query=ambient+music",
        )

    def test_follows_configured_prefix_symbol(self):
        with mock.patch.dict(plugin.os.environ, {"TARRAGON_PREFIX_SYMBOL": ":"}, clear=True):
            self.assertEqual(len(plugin.process(":g neural networks")), 1)
            self.assertEqual(plugin.process("@g neural networks"), [])

    def test_bare_token_without_symbol_is_ignored(self):
        with mock.patch.dict(plugin.os.environ, {}, clear=True):
            self.assertEqual(plugin.process("g neural networks"), [])

    def test_unknown_engine_returns_no_results(self):
        with mock.patch.dict(plugin.os.environ, {}, clear=True):
            self.assertEqual(plugin.process("@nope thing"), [])

    def test_known_engine_without_query_returns_hint(self):
        with mock.patch.dict(plugin.os.environ, {}, clear=True):
            results = plugin.process("@ddg")

        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["id"], "websearch-hint")
        self.assertIn("@ddg", results[0]["label"])

    def test_search_result_offers_query_replacement_for_other_engines(self):
        with mock.patch.dict(plugin.os.environ, {}, clear=True):
            result = plugin.process("@g ambient music")[0]

        self.assertEqual(result["actions"][0]["name"], "open")
        replacements = [
            action for action in result["actions"] if action.get("type") == "query_replace"
        ]
        self.assertEqual(
            {action["query"] for action in replacements},
            {
                "@yt ambient music",
                "@ddg ambient music",
                "@w ambient music",
                "@gh ambient music",
            },
        )
        self.assertEqual(
            {action["description"] for action in replacements},
            {
                "Search with YouTube",
                "Search with DuckDuckGo",
                "Search with Wikipedia",
                "Search with GitHub",
            },
        )

    def test_uses_resolved_plugin_prefix_when_available(self):
        with mock.patch.dict(
            plugin.os.environ,
            {"TARRAGON_PLUGIN_PREFIX": "@yt", "TARRAGON_PREFIX_SYMBOL": ":"},
            clear=True,
        ):
            self.assertEqual(plugin.query_prefix("yt"), "@yt")


if __name__ == "__main__":
    unittest.main()
