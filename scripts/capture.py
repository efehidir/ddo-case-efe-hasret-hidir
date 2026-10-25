"""Capture a reproducible command's real output and preserve its exit status."""
import datetime
from pathlib import Path
import subprocess
import sys

sys.stdout.reconfigure(encoding="utf-8", errors="replace")
destination = Path(sys.argv[1])
destination.parent.mkdir(parents=True, exist_ok=True)
command = sys.argv[2:]
with destination.open("w", encoding="utf-8") as output:
    output.write(f"UTC: {datetime.datetime.now(datetime.timezone.utc).isoformat()}\n")
    output.write("Command: " + " ".join(command) + "\n\n")
    output.flush()
    process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True, encoding="utf-8", errors="replace")
    for line in process.stdout:
        output.write(line)
        print(line, end="", flush=True)
    result = process.wait()
    output.write(f"\nExit code: {result}\n")
raise SystemExit(result)
