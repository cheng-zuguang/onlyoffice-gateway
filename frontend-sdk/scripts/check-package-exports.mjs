import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

const packageJson = JSON.parse(readFileSync("package.json", "utf8"));
const targets = new Map();

for (const field of ["main", "module", "types"]) {
  if (typeof packageJson[field] === "string") {
    targets.set(field, packageJson[field]);
  }
}

function collectExportTargets(value, trail) {
  if (typeof value === "string") {
    targets.set(trail, value);
    return;
  }

  if (!value || typeof value !== "object") {
    return;
  }

  for (const [key, nested] of Object.entries(value)) {
    collectExportTargets(nested, `${trail}.${key}`);
  }
}

collectExportTargets(packageJson.exports, "exports");

const missing = [];

for (const [name, target] of targets) {
  const normalizedTarget = target.startsWith("./") ? target.slice(2) : target;
  if (!existsSync(path.resolve(normalizedTarget))) {
    missing.push(`${name}: ${target}`);
  }
}

if (missing.length > 0) {
  console.error("Package entry targets do not exist:");
  for (const item of missing) {
    console.error(`- ${item}`);
  }
  process.exit(1);
}

console.log(`Verified ${targets.size} package entry targets.`);
