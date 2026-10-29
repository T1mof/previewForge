"""Copy canonical generated manifests into the chart; do not edit the copies."""
import argparse
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CHART = ROOT / "charts" / "previewforge"
SOURCES = [
    "rbac/role.yaml", "rbac/leader_election.yaml", "metrics-reader/role.yaml",
    "admission/role.yaml", "admission/namespaces.yaml", "admission/children.yaml",
    "admission/deployments.yaml", "admission/services.yaml", "admission/bindings.yaml",
    "admission/server.yaml",
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    pairs = [(ROOT / "config" / name, CHART / "files" / "generated" / name) for name in SOURCES]
    pairs += [(ROOT / "config/crd/bases/platform.previewforge.dev_previewenvironments.yaml",
               CHART / "crds/platform.previewforge.dev_previewenvironments.yaml")]
    pairs += [(ROOT / "LICENSE", CHART / "LICENSE")]
    stale = []
    for source, target in pairs:
        expected = source.read_bytes().replace(b"\r\n", b"\n")
        if args.check:
            if not target.exists() or target.read_bytes().replace(b"\r\n", b"\n") != expected:
                stale.append(str(target.relative_to(ROOT)))
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(expected)
    if stale:
        raise SystemExit("Stale chart assets; run scripts/sync-chart-assets.py:\n" + "\n".join(stale))
    print("Chart assets match canonical manifests" if args.check else "Chart assets synchronized")


if __name__ == "__main__":
    main()
