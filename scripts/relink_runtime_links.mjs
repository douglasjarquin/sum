/** Rewrite absolute in-tree runtime symlinks to relative ones so a renamed staging dir still resolves. */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export function relinkInsideRoot(linkPath, root) {
  let current;
  try {
    current = fs.readlinkSync(linkPath);
  } catch {
    return false;
  }
  if (!path.isAbsolute(current)) {
    return false;
  }
  const resolved = path.resolve(path.dirname(linkPath), current);
  const rootResolved = path.resolve(root);
  const prefix = rootResolved.endsWith(path.sep) ? rootResolved : rootResolved + path.sep;
  if (resolved !== rootResolved && !resolved.startsWith(prefix)) {
    return false;
  }
  const relative = path.relative(path.dirname(linkPath), resolved);
  fs.unlinkSync(linkPath);
  fs.symlinkSync(relative, linkPath);
  return true;
}

export function relinkRuntimeBin(root) {
  const bin = path.join(root, ".local", "bin");
  let names;
  try {
    names = fs.readdirSync(bin);
  } catch {
    return;
  }
  for (const name of names) {
    relinkInsideRoot(path.join(bin, name), root);
  }
}

const invoked = process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1]);
if (invoked && process.argv[2]) {
  relinkRuntimeBin(path.resolve(process.argv[2]));
}
