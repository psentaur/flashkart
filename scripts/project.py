#!/usr/bin/env python3
"""Commands for Mukthish's modules; no teammate build system is assumed."""
from __future__ import annotations
import argparse,hashlib,json,os,platform,shutil,subprocess,sys,tempfile,urllib.request,venv
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
MODULES=["services/admission","services/flash-sale","services/order","services/analytics","gateway","frontend","internal/platform","contracts","tests","scripts"]
TOOLS=ROOT/".tools"
VERSIONS=json.loads((ROOT/"dev/tool-versions.json").read_text())

def environment():
    result=os.environ.copy();result["GOTOOLCHAIN"]="go"+VERSIONS["go"]
    result["PATH"]=str(TOOLS/"bin")+os.pathsep+result.get("PATH","")
    result.setdefault("GOCACHE",str(ROOT/".cache/go-build"))
    result.setdefault("STATICCHECK_CACHE",str(ROOT/".cache/staticcheck"))
    return result

def run(args,cwd=ROOT,standalone=False,**kwargs):
    env=environment()
    if standalone:env["GOWORK"]="off"
    try:return subprocess.run([str(a) for a in args],cwd=cwd,env=env,check=True,**kwargs)
    except subprocess.CalledProcessError as exc:
        if exc.stdout:print(exc.stdout,file=sys.stderr)
        if exc.stderr:print(exc.stderr,file=sys.stderr)
        raise

def bootstrap():
    if not shutil.which("go"):raise SystemExit("Go is required; install the version in dev/tool-versions.json")
    TOOLS.joinpath("bin").mkdir(parents=True,exist_ok=True)
    version=run(["go","env","GOVERSION"],capture_output=True,text=True).stdout.strip()
    if version!="go"+VERSIONS["go"]:raise SystemExit("Wrong Go toolchain: "+version)
    run(["go","work","sync"])
    for module,pins in VERSIONS["workspace_pins"].items():
        if not pins:continue
        run(["go","mod","edit",*["-require="+package+"@"+version for package,version in pins.items()]], ROOT/module)
    for module in MODULES:
        env=environment();env["GOWORK"]="off"
        subprocess.run(["go","mod","download","all"],cwd=ROOT/module,env=env,check=True)
    plugins={"google.golang.org/protobuf/cmd/protoc-gen-go":VERSIONS["modules"]["google.golang.org/protobuf"],"google.golang.org/grpc/cmd/protoc-gen-go-grpc":VERSIONS["modules"]["google.golang.org/grpc/cmd/protoc-gen-go-grpc"],"honnef.co/go/tools/cmd/staticcheck":VERSIONS["modules"]["honnef.co/go/tools"]}
    for package,version in plugins.items():
        env=environment();env["GOBIN"]=str(TOOLS/"bin")
        subprocess.run(["go","install",package+"@"+version],cwd=ROOT,env=env,check=True)
    python=TOOLS/"contracts/bin/python"
    if not python.exists():venv.EnvBuilder(with_pip=True).create(TOOLS/"contracts")
    run([python,"-m","pip","install","--require-hashes","-r",ROOT/"scripts/requirements-contracts.lock"])
    arch={"x86_64":"x86_64","aarch64":"aarch64"}.get(platform.machine())
    if sys.platform!="linux" or not arch:raise SystemExit("Pinned Compose bootstrap supports Linux amd64/arm64; supply COMPOSE_COMMAND elsewhere")
    asset=VERSIONS["images"]["compose"]["assets"]["docker-compose-linux-"+arch]
    target=TOOLS/"bin/docker-compose"
    if not target.exists() or hashlib.sha256(target.read_bytes()).hexdigest()!=asset["digest"].removeprefix("sha256:"):
        with urllib.request.urlopen(asset["url"],timeout=90) as response:data=response.read()
        if "sha256:"+hashlib.sha256(data).hexdigest()!=asset["digest"]:raise SystemExit("Compose checksum mismatch")
        temporary=target.with_suffix(".download");temporary.write_bytes(data);temporary.chmod(0o755);temporary.replace(target)
    run([sys.executable,ROOT/"scripts/generate_contracts.py"])
    print("Pinned tools and all workspace modules are ready. Peer contract adoption remains pending.")

def quality(command):
    if command in ("fmt","fmt-check"):
        goroot=run(["go","env","GOROOT"],capture_output=True,text=True).stdout.strip()
        files=sorted(str(p) for module in MODULES for p in (ROOT/module).rglob("*.go"))
        fmt=[Path(goroot)/"bin/gofmt"]
        if command=="fmt":run(fmt+["-w",*files]);return
        result=run(fmt+["-l",*files],capture_output=True,text=True)
        if result.stdout.strip():print(result.stdout);raise SystemExit("gofmt changes required: run make fmt")
        return
    for module in MODULES:
        print(f"{command}: {module}",flush=True)
        if command=="typecheck":
            names=run(["go","list","-mod=readonly","-buildvcs=false","-f","{{.Name}}","./..."],ROOT/module,standalone=True,capture_output=True,text=True).stdout.splitlines()
            if "main" in names:
                with tempfile.TemporaryDirectory(prefix="flashkart-build-") as output:run(["go","build","-mod=readonly","-buildvcs=false","-o",output+"/","./..."],ROOT/module,standalone=True)
            else:run(["go","build","-mod=readonly","-buildvcs=false","./..."],ROOT/module,standalone=True)
        elif command=="test-unit":run(["go","test","-buildvcs=false","-race","./..."],ROOT/module)
        elif command=="lint":
            run(["go","vet","./..."],ROOT/module)
            tool=TOOLS/"bin/staticcheck"
            if not tool.exists():raise SystemExit("Run make bootstrap to install pinned staticcheck")
            run([tool,"./..."],ROOT/module)
        elif command=="verify-deps":run(["go","mod","verify"],ROOT/module)

def verify_pins():
    for module,expected in VERSIONS["module_checksums"].items():
        observed=json.loads(run(["go","mod","download","-json",module+"@"+expected["Version"]],capture_output=True,text=True).stdout)
        if any(observed.get(key)!=expected[key] for key in ("Version","Sum","GoModSum")):
            raise SystemExit("Pinned source checksum mismatch: "+module)
    selected=json.loads(run(["go","list","-m","-json","github.com/IBM/sarama"],capture_output=True,text=True).stdout)
    if selected["Version"]!=VERSIONS["modules"]["github.com/IBM/sarama"]:
        raise SystemExit("Workspace Sarama version drift")
    print("Pinned module/tool source checksums and one Sarama version verified")

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command",choices=["bootstrap","fmt","fmt-check","lint","typecheck","test-unit","verify-deps"])
    args=parser.parse_args()
    try:
        if args.command=="bootstrap":bootstrap()
        else:
            quality(args.command)
            if args.command=="verify-deps":verify_pins()
    except (subprocess.CalledProcessError,OSError) as exc:
        print(f"{args.command} failed: {exc}",file=sys.stderr);return 1
    return 0

if __name__=="__main__":raise SystemExit(main())
