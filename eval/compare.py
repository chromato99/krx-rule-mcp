#!/usr/bin/env python3
"""Compare returned top-5 retrieval evidence, including paired canonical source groups. Does not measure LLM answers."""
import argparse
import json
from pathlib import Path


def outcomes(cases):
    eligible = sum(c["evidence_eligible"] and not c["evidence_manual_review"] for c in cases)
    hit = sum(c["evidence_bundle_at_5_matched"] for c in cases)
    documents = [c for c in cases if c["expected_status"] == "supported"]
    document_hit = sum(0 < c.get("document_rank", 0) <= 5 for c in documents)
    return hit, eligible, document_hit, len(documents)


def rate(numerator, denominator):
    return f"{100 * numerator / denominator:.2f}%" if denominator else "n/a"


def main():
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reports", type=Path, nargs="+")
    parser.add_argument("--fixture", type=Path, default=root / "eval/golden/rag-v1.json")
    parser.add_argument("--manifest", type=Path, default=root.parent / "krx-rule-markdown/data/manifest.json")
    args = parser.parse_args()
    fixture = json.loads(args.fixture.read_text())
    manifest = json.loads(args.manifest.read_text())
    canonical = {d["id"]: d.get("source_id") or d["id"] for d in manifest["documents"]}
    names = {d["id"]: d["title"] for d in manifest["documents"]}
    targets = {c["id"]: {canonical[t["document_id"]] for t in c["expectation"]["targets"]} for c in fixture["cases"]}
    reports = []
    for path in args.reports:
        report = json.loads(path.read_text())
        if report.get("schema_version") != 2 or report["provenance"].get("evaluator_version") != "rag-retrieval-evaluator-v3":
            parser.error(f"{path}: requires retrieval evaluator v3 report; do not compare old answer metrics")
        if {c["id"] for c in report["cases"]} != set(targets):
            parser.error(f"{path}: case set differs from fixture")
        if reports:
            previous = reports[0][1]["provenance"]
            for field in ("case_set_sha256", "corpus_release_hash", "index_generation", "evaluator_version", "lexicon_digest", "retrieval_candidate_limit"):
                if report["provenance"].get(field) != previous.get(field):
                    parser.error(f"{path}: comparison {field} mismatch")
        reports.append((path.name, report))

    print("| Run | Language | Document Hit@5 | Evidence bundle Hit@5 | Evidence rate |")
    print("|---|---|---:|---:|---:|")
    for name, report in reports:
        for language, summary in sorted(report["language_summaries"].items()):
            hit, eligible, doc_hit, doc_eligible = outcomes([c for c in report["cases"] if (c.get("language") or "unspecified") == language])
            if (hit, eligible, doc_hit, doc_eligible) != (summary["evidence_bundle_hit_at_5"], summary["evidence_eligible"], summary["document_hit_at_5"], summary["document_eligible"]):
                parser.error(f"{name}/{language}: report summary does not match case outcomes")
            print(f"| {name} | {language} | {doc_hit}/{doc_eligible} | {hit}/{eligible} | {rate(hit, eligible)} |")

    print("\nCanonical-source diagnostics: multi-target questions may appear in multiple groups; do not sum these rows as independent samples.\n")
    print("| Source | " + " | ".join(name for name, _ in reports) + " |")
    print("|---|" + "---:|" * len(reports))
    sources = sorted(set().union(*targets.values()))
    for source in sources:
        row = []
        for _, report in reports:
            hit, eligible, doc_hit, doc_eligible = outcomes([c for c in report["cases"] if source in targets[c["id"]]])
            row.append(f"documents {doc_hit}/{doc_eligible}; evidence {hit}/{eligible}")
        print(f'| {source} {names.get(source, "")} | ' + " | ".join(row) + " |")


if __name__ == "__main__":
    main()
