import { execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { ensureDir } from './common.mjs';

const MODULE_DIR = path.dirname(fileURLToPath(import.meta.url));
const DEFAULT_CANONICAL_PATH = path.join(MODULE_DIR, 'perf-baselines.json');
const DEFAULT_REGISTRY_DIR = path.join(os.homedir(), '.attn-perf-registry');

// Best-effort sysctl read: attn only supports macOS, but harness unit tests
// and any future non-mac dev box should degrade gracefully rather than throw.
function readSysctl(key) {
  try {
    return execFileSync('sysctl', ['-n', key], { encoding: 'utf8' }).trim();
  } catch {
    return '';
  }
}

// Pure: hashes only the IDENTITY subset of a fingerprint (hardware + major OS
// version), deliberately excluding the full osRelease patch version so a
// routine macOS point-update does not orphan a machine's recorded baseline.
export function fingerprintKey({ hwModel, cpuBrand, cpuCount, arch, totalMemGb, osMajor }) {
  const identity = JSON.stringify({ hwModel, cpuBrand, cpuCount, arch, totalMemGb, osMajor });
  return crypto.createHash('sha256').update(identity).digest('hex').slice(0, 12);
}

export function getMachineFingerprint() {
  const arch = os.arch();
  const platform = os.platform();
  const osRelease = os.release();
  const hwModel = readSysctl('hw.model');
  const cpuBrand = readSysctl('machdep.cpu.brand_string') || os.cpus()[0]?.model || '';
  const cpuCount = os.cpus().length;
  const totalMemGb = Math.round(os.totalmem() / 1024 ** 3);
  const osMajor = parseInt(osRelease.split('.')[0], 10);

  const key = fingerprintKey({ hwModel, cpuBrand, cpuCount, arch, totalMemGb, osMajor });

  return { key, arch, platform, osRelease, hwModel, cpuBrand, cpuCount, totalMemGb };
}

// Pure regression check: a value BELOW baseline is always ok (an improvement,
// never a failure); only growth beyond tolerancePct above baseline fails. A
// missing baseline can never fail either — the first run on a machine always
// self-baselines rather than blocking.
export function compareToBaseline(value, baselineValue, { tolerancePct = 10 } = {}) {
  if (baselineValue == null) {
    return { ok: true, value, baseline: null, deltaPct: null, tolerancePct, reason: 'no-baseline' };
  }

  const deltaPct = Math.round(((value - baselineValue) / baselineValue) * 1000) / 10;
  const ok = deltaPct <= tolerancePct;

  return { ok, value, baseline: baselineValue, deltaPct, tolerancePct, reason: ok ? 'within-band' : 'regression' };
}

// Canonical (committed) baselines win over the local machine cache, so a
// hand-curated reference-machine entry always takes precedence once one is
// recorded for a key.
export function loadBaseline(fingerprintKeyValue, { registryDir = DEFAULT_REGISTRY_DIR, canonicalPath = DEFAULT_CANONICAL_PATH } = {}) {
  const canonical = readJsonIfExists(canonicalPath);
  if (canonical && Object.prototype.hasOwnProperty.call(canonical, fingerprintKeyValue)) {
    return canonical[fingerprintKeyValue];
  }

  const localPath = path.join(registryDir, `${fingerprintKeyValue}.json`);
  return readJsonIfExists(localPath);
}

// Writes only to the local per-machine cache. The canonical file is a
// hand-curated/committed artifact and is never written by this function.
export function saveBaseline(fingerprintKeyValue, baseline, { registryDir = DEFAULT_REGISTRY_DIR } = {}) {
  ensureDir(registryDir);
  const localPath = path.join(registryDir, `${fingerprintKeyValue}.json`);
  fs.writeFileSync(localPath, `${JSON.stringify(baseline, null, 2)}\n`);
}

function readJsonIfExists(filePath) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch {
    return null;
  }
}
