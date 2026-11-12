import filecmp
from pathlib import Path
import subprocess
import sys
import tempfile

protoc, tools, source = sys.argv[1:]
with tempfile.TemporaryDirectory() as output:
    subprocess.run(
        [
            protoc, "-I", "contracts/proto",
            f"--plugin=protoc-gen-go={tools}/protoc-gen-go",
            f"--plugin=protoc-gen-go-grpc={tools}/protoc-gen-go-grpc",
            f"--go_out={output}", "--go_opt=paths=source_relative",
            f"--go-grpc_out={output}", "--go-grpc_opt=paths=source_relative",
            source,
        ],
        check=True,
    )
    files = sorted(Path(output).rglob("*.go"))
    assert files
    for path in files:
        assert filecmp.cmp(path, Path("gen") / path.relative_to(output), shallow=False), path
print("Protobuf generation is reproducible.")
