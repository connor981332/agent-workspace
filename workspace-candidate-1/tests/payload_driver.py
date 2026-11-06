import json,sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from agent_workspace.payload import normalize
print(json.dumps(normalize(json.load(sys.stdin))))
