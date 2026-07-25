import os, sys


def build_bottle_block():
    bottle_vars = {
        "ROOT_URL": os.environ.get("ROOT_URL"),
        "ARM64_SONOMA_SHA": os.environ.get("ARM64_SONOMA_SHA"),
        "ARM64_TAHOE_SHA": os.environ.get("ARM64_TAHOE_SHA"),
        "CELLAR": os.environ.get("CELLAR"),
    }
    set_count = sum(1 for v in bottle_vars.values() if v)
    if set_count == 0:
        return ""
    if set_count != len(bottle_vars):
        missing = [name for name, value in bottle_vars.items() if not value]
        sys.exit(
            "error: partial bottle env — set all of ROOT_URL, ARM64_SONOMA_SHA, "
            "ARM64_TAHOE_SHA, CELLAR or none of them; missing: "
            f"{', '.join(missing)}"
        )
    root_url = bottle_vars["ROOT_URL"]
    sonoma = bottle_vars["ARM64_SONOMA_SHA"]
    tahoe = bottle_vars["ARM64_TAHOE_SHA"]
    cellar = bottle_vars["CELLAR"]
    return (
        "  # Bottles are poured by arch; brew falls back to an older-OS bottle of\n"
        "  # the same arch on newer macOS, and to a source build if none match.\n"
        "  bottle do\n"
        f'    root_url "{root_url}"\n'
        f'    sha256 cellar: :{cellar}, arm64_sonoma: "{sonoma}"\n'
        f'    sha256 cellar: :{cellar}, arm64_tahoe:  "{tahoe}"\n'
        "  end\n"
        "\n"
    )


template_path, out_path = sys.argv[1], sys.argv[2]
url = os.environ["URL"]
sha256 = os.environ["SHA256"]
bottle_block = build_bottle_block()

with open(template_path) as f:
    lines = f.readlines()

out = []
for line in lines:
    if line.strip() == "__BOTTLE_BLOCK__":
        if bottle_block:
            out.append(bottle_block)
        continue
    out.append(line.replace("__URL__", url).replace("__SHA256__", sha256))

rendered = "".join(out)

if bottle_block and "bottle do" not in rendered:
    sys.exit(
        "error: bottle block was expected but 'bottle do' is missing from the "
        "rendered output — check that __BOTTLE_BLOCK__ is still present in the template"
    )

if "__" in rendered:
    sys.exit(
        "error: unresolved placeholder ('__') remains in the rendered output"
    )

with open(out_path, "w") as f:
    f.write(rendered)
