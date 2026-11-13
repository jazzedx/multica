import assert from "node:assert/strict";
import { test } from "node:test";
import { ESLint } from "eslint";
import base from "./base.js";

const BANNED = [
  "lucide-react",
  "react-icons/fa",
  "@heroicons/react/24/outline",
  "@tabler/icons-react",
  "@radix-ui/react-icons",
  "@phosphor-icons/react/dist/ssr",
];
const ALLOWED = ["@multica/ui/icons", "react-icons-extra", "@radix-ui/react-dialog", "@heroicons/reactive"];

async function restricted(eslint, filePath, source) {
  const [result] = await eslint.lintText(`import { X } from "${source}";\nexport { X };\n`, { filePath });
  return result.messages.filter((m) => m.ruleId === "no-restricted-imports").length;
}

test("base config bans icon libraries and their subpaths", async () => {
  const eslint = new ESLint({ overrideConfigFile: true, overrideConfig: base });
  for (const source of BANNED) assert.equal(await restricted(eslint, "src/icon.ts", source), 1, source);
});

test("base config allows @multica/ui/icons and look-alike packages", async () => {
  const eslint = new ESLint({ overrideConfigFile: true, overrideConfig: base });
  for (const source of ALLOWED) assert.equal(await restricted(eslint, "src/icon.ts", source), 0, source);
});
