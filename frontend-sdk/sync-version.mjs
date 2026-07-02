import { readFileSync, writeFileSync } from "fs";
import { join, dirname } from "path";
import { fileURLToPath } from "url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const versionPath = join(__dirname, "..", "VERSION");
const version = readFileSync(versionPath, "utf-8").trim().replace(/^v/, "");

// Update package.json
const pkgPath = join(__dirname, "package.json");
const pkg = JSON.parse(readFileSync(pkgPath, "utf-8"));
pkg.version = version;
writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + "\n");
console.log(`package.json version set to ${version}`);

// Update exported VERSION in component
const compPath = join(__dirname, "src", "OnlyOfficeEditor.tsx");
let comp = readFileSync(compPath, "utf-8");
comp = comp.replace(/export const VERSION = ".*"/, `export const VERSION = "${version}"`);
writeFileSync(compPath, comp);
console.log(`OnlyOfficeEditor VERSION set to ${version}`);
