import { describe, expect, it } from 'vitest';
import { appPids, parseFootprint, parseGraphicsRegions, parseVmmapSummary } from './perfMeasure.mjs';

// Verbatim `vmmap` (non-summary) rows: two pane-sized surfaces, one volatile
// (0K dirty), one compositing tile, and one small layer.
const REGIONS = `owned unmapped (graphics)    unmapped-unmapped     [ 22.6M  22.6M  22.6M     0K] rw-/rw- SM=PRV PURGE=N  owned physical footprint (unmapped) (graphics)
owned unmapped (graphics)    unmapped-unmapped     [ 27.9M  27.9M  27.9M     0K] rw-/rw- SM=PRV PURGE=N  owned physical footprint (unmapped) (graphics)
owned unmapped (graphics)    unmapped-unmapped     [ 22.6M  22.6M     0K     0K] rw-/rw- SM=PRV PURGE=V  owned physical footprint (unmapped) (graphics)
owned unmapped (graphics)    unmapped-unmapped     [ 4144K  4144K  4144K     0K] rw-/rw- SM=PRV PURGE=N  owned physical footprint (unmapped) (graphics)
owned unmapped (graphics)    unmapped-unmapped     [   64K    64K    64K     0K] rw-/rw- SM=PRV PURGE=N  owned physical footprint (unmapped) (graphics)
MALLOC_SMALL                 0x117000000-0x117800000 [ 8192K  1024K  1024K    0K] rw-/rwx SM=PRV
`;

describe('parseGraphicsRegions', () => {
  it('counts every graphics region and ignores other region types', () => {
    expect(parseGraphicsRegions(REGIONS).regionCount).toBe(5);
  });

  it('separates pane-sized surfaces from compositing tiles and small layers', () => {
    const { largeCount, histogram } = parseGraphicsRegions(REGIONS);
    expect(largeCount).toBe(3);
    expect(histogram).toEqual({ '22.6': 2, '27.9': 1 });
  });

  it('sums dirty separately from virtual, so a volatile surface is visible', () => {
    const { largeDirtyMb, largeVirtualMb } = parseGraphicsRegions(REGIONS);
    // The third surface is resident but purgeable: it counts toward virtual and
    // not toward dirty, which is how reclaimable GPU memory shows up.
    expect(largeDirtyMb).toBeCloseTo(50.5, 1);
    expect(largeVirtualMb).toBeCloseTo(73.1, 1);
  });

  it('returns empty results rather than throwing on unparseable input', () => {
    expect(parseGraphicsRegions('').regionCount).toBe(0);
    expect(parseGraphicsRegions('nonsense').largeCount).toBe(0);
  });
});

// Verbatim rows from `vmmap --summary` on a packaged attn WebContent process,
// including the trailing MALLOC ZONE table that must not be parsed as regions.
const SUMMARY = `Process:         com.apple.WebKit.WebContent [44033]
Physical footprint:         1.2G
Physical footprint (peak):  3.5G
----

                                VIRTUAL RESIDENT    DIRTY  SWAPPED VOLATILE   NONVOL    EMPTY   REGION
REGION TYPE                        SIZE     SIZE     SIZE     SIZE     SIZE     SIZE     SIZE    COUNT (non-coalesced)
===========                     ======= ========    =====  ======= ========   ======    =====  =======
JS JIT generated code            512.0M    14.9M    9600K    2384K       0K       0K       0K        3
JS VM Gigacage                     1.0G    20.4M    6720K     384K       0K       0K       0K        5
JS VM Gigacage (reserved)         14.9G       0K       0K       0K       0K       0K       0K        3         reserved VM address space (unallocated)
MALLOC_LARGE                      2464K      48K      48K    2416K       0K       0K       0K        1         see MALLOC ZONE table below
MALLOC_SMALL                      66.2M    13.9M    10.4M    3008K       0K       0K       0K     2275         see MALLOC ZONE table below
MALLOC_TINY                       4096K     240K     240K      48K       0K       0K       0K        1         see MALLOC ZONE table below
Memory Tag 241                     320K       0K       0K      80K       0K       0K       0K        4
dyld private memory                160K       56       56       0K       0K       0K       0K        4
VM_ALLOCATE (graphics)             128K     128K     128K       0K       0K     128K       0K        5
WebKit Malloc                     23.2G   709.7M   385.1M    25.9M       0K       0K       0K      402
WebKit Malloc metadata           128.1M    4272K    4144K     368K       0K       0K       0K        2
__CTF                               824      824       0K       0K       0K       0K       0K        1
owned unmapped (graphics)          1.0G     1.0G   665.5M    43.6M       0K       0K       0K      141
===========                     ======= ========    =====  ======= ========   ======    =====  =======
TOTAL                             75.7G     2.4G     1.1G    79.2M       0K    10.0M    2608K     9307
TOTAL, minus reserved VM space    29.1G     2.4G     1.1G    79.2M       0K    10.0M    2608K     9307

                                          VIRTUAL   RESIDENT      DIRTY    SWAPPED ALLOCATION      BYTES DIRTY+SWAP          REGION
MALLOC ZONE                                  SIZE       SIZE       SIZE       SIZE      COUNT  ALLOCATED  FRAG SIZE  % FRAG   COUNT
===========                               =======  =========  =========  =========  =========  =========  =========  ======  ======
WebKit Malloc_0x10610e678                    3.8G     741.8M     405.5M      27.6M     632159      20.4G         0K      0%     203
`;

describe('parseVmmapSummary', () => {
  it('reads dirty and resident megabytes for a region', () => {
    const { byRegion } = parseVmmapSummary(SUMMARY);
    expect(byRegion['WebKit Malloc']).toEqual({ residentMb: 709.7, dirtyMb: 385.1 });
  });

  it('keeps a multi-word region name intact', () => {
    const { byRegion } = parseVmmapSummary(SUMMARY);
    expect(byRegion['owned unmapped (graphics)']).toEqual({ residentMb: 1024, dirtyMb: 665.5 });
  });

  it('converts each size suffix to megabytes', () => {
    const { byRegion } = parseVmmapSummary(SUMMARY);
    // K, M and G all appear in the DIRTY column above.
    expect(byRegion['JS JIT generated code'].dirtyMb).toBeCloseTo(9.4, 1);
    expect(byRegion['MALLOC_SMALL'].dirtyMb).toBe(10.4);
    expect(byRegion['owned unmapped (graphics)'].residentMb).toBe(1024);
  });

  it('sums the slices a memory receipt reports', () => {
    const { slices } = parseVmmapSummary(SUMMARY);
    expect(slices.graphics).toBe(665.6); // owned unmapped + VM_ALLOCATE
    expect(slices.webkitMalloc).toBe(389.1); // WebKit Malloc + its metadata
    expect(slices.jsHeap).toBeCloseTo(15.9, 1);
  });

  it('excludes both summary rows from the region map and the dirty total', () => {
    const { byRegion, totalDirtyMb } = parseVmmapSummary(SUMMARY);
    expect(Object.keys(byRegion).filter((name) => name.startsWith('TOTAL'))).toEqual([]);
    // Sum of the region rows above. Counting either summary row (1.1G each)
    // would roughly triple it.
    expect(totalDirtyMb).toBeCloseTo(1081.4, 1);
  });

  it('stops before the MALLOC ZONE table, whose rows have a different arity', () => {
    const { byRegion } = parseVmmapSummary(SUMMARY);
    expect(Object.keys(byRegion).some((name) => name.includes('0x10610e678'))).toBe(false);
  });

  it('keeps a row that carries trailing prose after the region count', () => {
    // These rows were silently dropped while the parser anchored on the last
    // column, which read a 0 MB malloc slice as if it were measured.
    const { byRegion, slices } = parseVmmapSummary(SUMMARY);
    expect(byRegion.MALLOC_SMALL).toEqual({ residentMb: 13.9, dirtyMb: 10.4 });
    expect(slices.malloc).toBeCloseTo(10.7, 1);
  });

  it('keeps a region name that ends in a bare number', () => {
    const { byRegion } = parseVmmapSummary(SUMMARY);
    expect(byRegion['Memory Tag 241']).toEqual({ residentMb: 0, dirtyMb: 0 });
  });

  it('keeps a multi-word name followed by unsuffixed byte counts', () => {
    const { byRegion } = parseVmmapSummary(SUMMARY);
    expect(byRegion['dyld private memory']).toEqual({ residentMb: 0, dirtyMb: 0 });
  });

  it('excludes a reserved-address-space row from its slice', () => {
    // `JS VM Gigacage (reserved)` is unallocated VM, not memory in use.
    const { slices } = parseVmmapSummary(SUMMARY);
    expect(slices.jsHeap).toBeCloseTo(15.9, 1);
  });

  it('parses a raw byte count with no size suffix, rounding sub-MB to 0.0', () => {
    const { byRegion } = parseVmmapSummary(SUMMARY);
    // 824 bytes. The row is kept (not dropped as unparseable) but reports 0.0:
    // a memory receipt is read in megabytes, so sub-MB regions are noise.
    expect(byRegion.__CTF).toEqual({ residentMb: 0, dirtyMb: 0 });
  });

  it('returns empty results rather than throwing on unparseable input', () => {
    expect(parseVmmapSummary('').byRegion).toEqual({});
    expect(parseVmmapSummary('garbage\nmore garbage').totalDirtyMb).toBe(0);
  });
});

describe('parseFootprint', () => {
  it('reads the physical footprint, not the peak line below it', () => {
    // 1.2G vs the 3.5G peak. Taking the peak would report a number the process
    // has not held since some earlier burst.
    expect(parseFootprint(SUMMARY)).toBeCloseTo(1228.8, 1);
  });

  it('is null when the header is absent, so a missing value is never a zero', () => {
    expect(parseFootprint('no header here')).toBeNull();
  });

  it('travels with the region breakdown', () => {
    expect(parseVmmapSummary(SUMMARY).footprintMb).toBeCloseTo(1228.8, 1);
  });
});

describe('appPids', () => {
  // A headless `claude -p` the daemon spawns for classification is a child of
  // the daemon, so a process-tree walk sweeps it into the snapshot. It is a
  // separate program; counting its ~450MB as the app's makes every app number
  // wrong in the same direction.
  const SNAP = {
    byClass: {
      app: { pids: [{ pid: 10 }] },
      webkit_webcontent: { pids: [{ pid: 11 }] },
      webkit_gpu: { pids: [{ pid: 12 }] },
      webkit_networking: { pids: [{ pid: 13 }] },
      daemon: { pids: [{ pid: 20 }] },
      pty_worker: { pids: [{ pid: 21 }, { pid: 22 }] },
      shell: { pids: [{ pid: 23 }] },
      '/Users/victor/.l': { pids: [{ pid: 24 }] },
    },
  };

  it('selects the Tauri process and its WebKit processes', () => {
    expect(appPids(SNAP).sort((a, b) => a - b)).toEqual([10, 11, 12, 13]);
  });

  it('excludes the daemon, its workers, session shells, and spawned agents', () => {
    const pids = appPids(SNAP);
    for (const notApp of [20, 21, 22, 23, 24]) expect(pids).not.toContain(notApp);
  });

  it('returns nothing rather than throwing when a snapshot is missing classes', () => {
    expect(appPids({})).toEqual([]);
    expect(appPids(undefined)).toEqual([]);
  });
});
