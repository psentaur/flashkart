#!/usr/bin/env python3
"""Generate or compare only Mukthish's Inventory Go bindings."""
from __future__ import annotations
import argparse,os,subprocess,tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument("--check",action="store_true");args=parser.parse_args()
    python=ROOT/".tools/contracts/bin/python"
    if not python.exists():raise SystemExit("Run make bootstrap to install pinned protobuf tools")
    env=os.environ.copy();env["PATH"]=str(ROOT/".tools/bin")+os.pathsep+env.get("PATH","")
    include=subprocess.check_output([python,"-c","import grpc_tools,pathlib;print(pathlib.Path(grpc_tools.__file__).parent/'_proto')"],text=True).strip()
    with tempfile.TemporaryDirectory(prefix="flashkart-protobuf-") as directory:
        output=Path(directory)
        subprocess.run([python,"-m","grpc_tools.protoc","-I"+str(ROOT/"contracts"),"-I"+include,"--go_out=paths=source_relative:"+directory,"--go-grpc_out=paths=source_relative:"+directory,str(ROOT/"contracts/inventory.proto")],env=env,check=True)
        target=ROOT/"contracts/gen/inventory/v1"
        (output/"doc.go").write_text("// Package inventoryv1 contains generated Inventory RPC bindings.\npackage inventoryv1\n")
        generated=sorted(output.glob("*.go"));assert len(generated)==3
        if args.check:
            if not target.exists() or {p.name for p in target.glob('*.go')}!={p.name for p in generated}:raise SystemExit("Generated Inventory binding set differs; run make generate")
            for source in generated:
                if (target/source.name).read_bytes()!=source.read_bytes():raise SystemExit("Generated binding drift: "+source.name)
            print("Generated Inventory bindings match pinned generators")
        else:
            target.mkdir(parents=True,exist_ok=True)
            for source in generated:(target/source.name).write_bytes(source.read_bytes())
    return 0

if __name__=="__main__":raise SystemExit(main())
