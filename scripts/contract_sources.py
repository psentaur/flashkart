"""Expected canonical copies of feature contracts; only index links need relocation."""
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
SOURCE=ROOT/'specs/001-flash-sale-platform/contracts'
NAMES=('README.md','public.openapi.json','internal.openapi.json','catalog-store.openapi.json','inventory.proto','events.schema.json','examples.json')

def expected_content(name):
    content=(SOURCE/name).read_bytes()
    if name=='README.md':
        content=content.replace(b'](../data-model.md)',b'](../specs/001-flash-sale-platform/data-model.md)')
    return content
