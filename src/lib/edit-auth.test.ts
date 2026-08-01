import assert from "node:assert/strict";
import test from "node:test";

import { editTokenMatches } from "./edit-auth.ts";

test("editTokenMatches compares fixed-length hashes", () => {
  assert.equal(editTokenMatches("correct horse", "correct horse"), true);
  assert.equal(editTokenMatches("wrong", "correct horse"), false);
  assert.equal(editTokenMatches("", "correct horse"), false);
  assert.equal(editTokenMatches("", ""), false);
});
