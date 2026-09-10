"""Local graphify extraction with fixes for this repo.

Run with graphify's interpreter from the repo root:
    $(cat graphify-out/.graphify_python) scripts/graphify_fix_extraction.py

Writes graphify-out/.graphify_detect.json and .graphify_extract.json, ready for
the normal build/cluster/report steps. Fixes applied on top of stock graphify:

1. Svelte: stock graphify parses the whole .svelte file with the JavaScript
   grammar, so the markup and `lang="ts"` annotations fail to parse and most
   script symbols are lost. Here everything outside <script> is blanked
   (line numbers kept) and the script is parsed with the TypeScript grammar.
2. Dangling edges: imports of this module's own packages point at a real
   package node; imports of external packages (Go stdlib, npm) have no node,
   so the edge is dropped and recorded on the file node as `external_imports`.
3. Self-loops: a file `contains` itself is dropped. SQL self-references
   (parent_id foreign keys) are real and kept.
4. Collapsed edges: repeated edges between one node pair are merged into one,
   with weight = count and every source line kept in `source_locations`.
"""
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

import graphify.extract as gx
from graphify.cache import check_semantic_cache
from graphify.detect import detect

ROOT = Path(".")
SPEC = str(Path.home() / ".claude/skills/graphify/references/extraction-spec.md")
OUT = Path("graphify-out")

_extract_generic = getattr(gx, "_extract_generic", None)
if _extract_generic is None:
    from graphify.extractors.engine import _extract_generic
_make_id = gx._make_id

SCRIPT_RE = re.compile(r"(<script\b([^>]*)>)([\s\S]*?)(</script\s*>)", re.IGNORECASE)


def _mask_outside_scripts(src: str) -> tuple[str, bool]:
    """Blank every character outside <script> bodies, keeping newlines."""
    out = []
    pos = 0
    is_ts = False
    for m in SCRIPT_RE.finditer(src):
        out.append(re.sub(r"[^\n]", " ", src[pos:m.start(3)]))
        out.append(m.group(3))
        pos = m.end(3)
        if re.search(r"""lang\s*=\s*['"]ts['"]""", m.group(2)):
            is_ts = True
    out.append(re.sub(r"[^\n]", " ", src[pos:]))
    return "".join(out), is_ts


def extract_svelte_fixed(path: Path) -> dict:
    src = path.read_text(encoding="utf-8", errors="replace")
    masked, is_ts = _mask_outside_scripts(src)
    config = gx._TS_CONFIG if is_ts else gx._JS_CONFIG
    return _extract_generic(path, config, source_override=masked.encode("utf-8"))


gx._DISPATCH[".svelte"] = extract_svelte_fixed


def go_module_name() -> str | None:
    gomod = ROOT / "go.mod"
    if not gomod.exists():
        return None
    m = re.search(r"^module\s+(\S+)", gomod.read_text(), re.MULTILINE)
    return m.group(1) if m else None


def main() -> None:
    OUT.mkdir(exist_ok=True)
    det = detect(ROOT)
    (OUT / ".graphify_detect.json").write_text(json.dumps(det, ensure_ascii=False), encoding="utf-8")

    code = []
    for f in det["files"].get("code", []):
        p = Path(f)
        code.extend(gx.collect_files(p) if p.is_dir() else [p])
    ast = gx.extract(code, cache_root=ROOT)

    sem_files = [f for c in ("document", "paper", "image") for f in det["files"].get(c, [])]
    cn, ce, ch, uncached = check_semantic_cache(sem_files, root=".", prompt_file=SPEC)
    if uncached:
        print(f"WARNING: {len(uncached)} doc/image file(s) not in the semantic cache; "
              f"run the full /graphify pipeline to extract them: {uncached}", file=sys.stderr)

    nodes = list(ast["nodes"])
    ids = {n["id"] for n in nodes}
    for n in cn:
        if n["id"] not in ids:
            nodes.append(n)
            ids.add(n["id"])
    edges = ast["edges"] + ce
    by_id = {n["id"]: n for n in nodes}
    stats = defaultdict(int)

    # 2a. Map imports of this Go module's own packages to package nodes.
    module = go_module_name()
    pkg_map = {}
    if module:
        for n in nodes:
            sf = n.get("source_file") or ""
            if sf.endswith(".go") and n["id"] == _make_id(sf.rsplit(".", 1)[0]):
                pkg_dir = str(Path(sf).parent).replace("\\", "/")
                import_path = module if pkg_dir == "." else f"{module}/{pkg_dir}"
                pkg_map.setdefault(_make_id("go", "pkg", import_path), (import_path, pkg_dir, []))[2].append(n["id"])
        for pkg_id, (import_path, pkg_dir, file_ids) in pkg_map.items():
            if not any(e["target"] == pkg_id for e in edges):
                continue
            if pkg_id not in ids:
                nodes.append({"id": pkg_id, "label": f"{import_path} (Go package)", "file_type": "code",
                              "source_file": f"{pkg_dir}/", "confidence": "EXTRACTED"})
                ids.add(pkg_id)
                stats["package nodes added"] += 1
            for fid in file_ids:
                edges.append({"source": pkg_id, "target": fid, "relation": "contains",
                              "confidence": "EXTRACTED", "confidence_score": 1.0,
                              "source_file": by_id[fid].get("source_file")})

    # 2b. Drop edges to external packages, recording them on the importing node.
    kept = []
    for e in edges:
        if e["source"] in ids and e["target"] in ids:
            kept.append(e)
            continue
        stats["external edges dropped"] += 1
        if e["source"] in ids:
            by_id_node = next(n for n in nodes if n["id"] == e["source"])
            ext = by_id_node.setdefault("external_imports", [])
            name = re.sub(r"^(go_pkg_|ref_)", "", e["target"])
            if name not in ext:
                ext.append(name)
    edges = kept

    # 3. Drop a file `contains` itself; keep genuine self-references.
    kept = []
    for e in edges:
        if e["source"] == e["target"] and e.get("relation") == "contains":
            stats["contains self-loops dropped"] += 1
            continue
        kept.append(e)
    edges = kept

    # 4. Merge repeated edges per unordered node pair.
    generic = {"references", "conceptually_related_to", "semantically_similar_to"}
    groups: dict[frozenset, list] = defaultdict(list)
    order = []
    for e in edges:
        key = frozenset((e["source"], e["target"]))
        if key not in groups:
            order.append(key)
        groups[key].append(e)
    merged = []
    for key in order:
        group = groups[key]
        if len(group) == 1:
            merged.append(group[0])
            continue
        stats["duplicate edges merged"] += len(group) - 1
        specific = [e for e in group if e.get("relation") not in generic]
        best = dict((specific or group)[0])
        best["weight"] = float(len(group))
        locs = sorted({e.get("source_location") for e in group if e.get("source_location")})
        if locs:
            best["source_locations"] = locs
        rels = sorted({e.get("relation") for e in group})
        if len(rels) > 1:
            best["relations"] = rels
        if any(e.get("confidence") == "EXTRACTED" for e in group):
            best["confidence"] = "EXTRACTED"
            best["confidence_score"] = 1.0
        merged.append(best)
    edges = merged

    out = {"nodes": nodes, "edges": edges, "hyperedges": ch, "input_tokens": 0, "output_tokens": 0}
    (OUT / ".graphify_extract.json").write_text(json.dumps(out, ensure_ascii=False), encoding="utf-8")
    print(f"Extraction: {len(nodes)} nodes, {len(edges)} edges")
    for k, v in stats.items():
        print(f"  {k}: {v}")


if __name__ == "__main__":
    main()
