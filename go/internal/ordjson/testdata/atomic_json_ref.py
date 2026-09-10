import importlib.util
import json
import sys

spec = importlib.util.spec_from_file_location("sumctl_ref", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

output_path = sys.argv[2]
value = json.loads(sys.argv[3])
module.atomic_json(output_path, value)
