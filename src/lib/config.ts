import { homedir } from "node:os";
import path from "node:path";

export const VAULT_PATH =
  process.env.MDHUB_VAULT_PATH ||
  path.join(homedir(), "Documents", "Obsidian Vault");

export const PUBLIC_BASE_URL =
  process.env.MDHUB_PUBLIC_BASE_URL ||
  "http://localhost:10001/mdhub";
