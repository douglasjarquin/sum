import json
import sys

output_path = sys.argv[1]
value = json.loads(sys.argv[2])
with open(output_path, "w", encoding="utf-8") as out:
    json.dump(value, out, indent=2, ensure_ascii=True)
    out.write("\n")
