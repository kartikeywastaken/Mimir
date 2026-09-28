import unittest
from unittest.mock import patch
import laya_solve


class FakeAgent:
    def predict(self, state, schema):
        self.state = state
        return {"answers": {"q": {"choice": "Y) Yes", "confidence": 0.9}}}


class CheckboxContextTest(unittest.TestCase):
    def test_individual_option_reaches_model(self):
        agent = FakeAgent()
        with patch.object(laya_solve, "get_agent", return_value=(agent, "laya-coreml", 96)):
            result = laya_solve.solve_one({
                "state": "Which are primary colors of light?",
                "question": "Should Green be selected?",
                "choices": [{"label": "Y", "text": "Yes"}, {"label": "N", "text": "No"}],
            })
        self.assertEqual(result["answer"], "Y")
        self.assertIn("Should Green be selected?", agent.state)


if __name__ == "__main__":
    unittest.main()
