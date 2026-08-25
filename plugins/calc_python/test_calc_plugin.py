import unittest

import calc_plugin as plugin


class CalcPluginTest(unittest.TestCase):
    def test_arithmetic_expression_still_works(self):
        self.assertEqual(plugin.process("2 + 2")[0]["id"], "4")
        self.assertEqual(plugin.process("log10(100)")[0]["id"], "2")
        self.assertEqual(plugin.process("0x10")[0]["id"], "16")

    def test_solves_linear_equation_with_implicit_multiplication(self):
        result = plugin.process("2x + 4 = 10")

        self.assertEqual([item["id"] for item in result], ["3"])
        self.assertEqual(result[0]["label"], "2x + 4 = 10 -> x = 3")

    def test_solves_equation_with_x_on_both_sides(self):
        self.assertEqual(plugin.process("3x - 2 = x + 8")[0]["id"], "5")

    def test_solves_quadratic_equation(self):
        self.assertEqual(
            [item["id"] for item in plugin.process("x^2 = 4")],
            ["-2", "2"],
        )

    def test_supports_parenthesized_implicit_multiplication(self):
        self.assertEqual(plugin.process("2(x + 1) = 8")[0]["id"], "3")

    def test_returns_no_result_for_unsupported_equations(self):
        self.assertEqual(plugin.process("x^2 + 1 = 0"), [])
        self.assertEqual(plugin.process("x^3 = 8"), [])
        self.assertEqual(plugin.process("x = x"), [])

    def test_rejects_unsafe_expression(self):
        self.assertEqual(plugin.process("x = __import__('os').system('true')"), [])


if __name__ == "__main__":
    unittest.main()
