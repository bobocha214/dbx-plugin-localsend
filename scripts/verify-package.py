"""Verify release package metadata, executable architecture and Unix permissions."""
import argparse
import hashlib
import json
import pathlib
import stat
import struct
import zipfile


def verify(path):
    path = pathlib.Path(path)
    artifact = json.loads(path.with_suffix('.artifact.json').read_text(encoding='utf-8-sig'))
    target = artifact['target']
    platform, arch = target.split('-')
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    assert artifact['sha256'] == digest
    assert artifact['size'] == path.stat().st_size
    assert artifact['url'] == path.name
    assert pathlib.Path(str(path) + '.sha256').read_text().split()[0] == digest
    with zipfile.ZipFile(path) as archive:
        manifest = json.loads(archive.read('manifest.json').decode('utf-8-sig'))
        assert path.name == f"{manifest['id']}-{manifest['version']}-{target}.dbxp"
        binary = 'bin/dbx-plugin-localsend' + ('.exe' if platform == 'windows' else '')
        assert manifest['entrypoints']['backend']['executable'] == binary
        checks = json.loads(archive.read('checksums.json').decode('utf-8-sig'))
        assert checks['algorithm'] == 'sha256'
        assert set(checks['files']) == set(archive.namelist()) - {'checksums.json'}
        for name, expected in checks['files'].items():
            assert '\\' not in name and not name.startswith('/') and '..' not in name.split('/')
            assert hashlib.sha256(archive.read(name)).hexdigest() == expected, name
        data = archive.read(binary)
        if platform == 'windows':
            assert data[:2] == b'MZ'
            offset = struct.unpack_from('<I', data, 0x3c)[0]
            assert data[offset:offset + 4] == b'PE\0\0'
            assert struct.unpack_from('<H', data, offset + 4)[0] == {'x64': 0x8664, 'arm64': 0xaa64}[arch]
        elif platform == 'linux':
            assert data[:6] == b'\x7fELF\x02\x01'
            assert struct.unpack_from('<H', data, 18)[0] == {'x64': 62, 'arm64': 183}[arch]
        elif platform == 'darwin':
            assert data[:4] == b'\xcf\xfa\xed\xfe'
            assert struct.unpack_from('<I', data, 4)[0] == {'x64': 0x01000007, 'arm64': 0x0100000c}[arch]
        else:
            raise AssertionError(f'Unsupported platform: {platform}')
        if platform != 'windows':
            assert archive.getinfo(binary).create_system == 3
            mode = archive.getinfo(binary).external_attr >> 16
            assert stat.S_ISREG(mode) and mode & 0o111 == 0o111, oct(mode)
    print(f'Verified {target}: architecture, executable permissions, manifest, metadata and all checksums.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('packages', nargs='+')
    for package in parser.parse_args().packages:
        verify(package)
