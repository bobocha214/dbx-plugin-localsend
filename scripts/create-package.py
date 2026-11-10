"""Create a DBX ZIP with portable paths and Unix executable mode metadata."""
import argparse
import json
import pathlib
import zipfile

parser = argparse.ArgumentParser()
parser.add_argument('stage')
parser.add_argument('package')
args = parser.parse_args()
stage = pathlib.Path(args.stage)
manifest = json.loads((stage / 'manifest.json').read_text(encoding='utf-8-sig'))
binary = manifest['entrypoints']['backend']['executable']
with zipfile.ZipFile(args.package, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
    for path in sorted(stage.rglob('*')):
        if not path.is_file():
            continue
        name = path.relative_to(stage).as_posix()
        info = zipfile.ZipInfo.from_file(path, name)
        # Go's archive/zip reads Unix modes only when the creator OS is Unix.
        info.create_system = 3
        info.external_attr = (0o100755 if name == binary and not binary.endswith('.exe') else 0o100644) << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        with path.open('rb') as source, archive.open(info, 'w', force_zip64=True) as target:
            while chunk := source.read(1024 * 1024):
                target.write(chunk)
