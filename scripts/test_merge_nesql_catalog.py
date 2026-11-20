import gzip
import hashlib
import json
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from merge_nesql_catalog import merge_catalog, read_export


def fixture(root, name, items):
    root.mkdir()
    files = []
    for kind, rows in {
        'items': items,
        'assets': [{'id': name+'-image', 'path': 'assets/'+name+'.png', 'width': 16, 'height': 16}],
        'strings': [{'id': name+'-text', 'locale': 'zh_CN', 'text': name+'中文'}],
    }.items():
        path = 'records/'+kind+'/part.jsonl.gz'
        decoded = b''.join(json.dumps(row).encode()+b'\n' for row in rows)
        data = gzip.compress(decoded)
        (root/path).parent.mkdir(parents=True)
        (root/path).write_bytes(data)
        files.append({'path': path, 'kind': kind, 'encoding': 'jsonl.gzip', 'bytes': len(data), 'decodedBytes': len(decoded), 'rows': len(rows), 'sha256': hashlib.sha256(data).hexdigest()})
    (root/'assets').mkdir()
    data = b'fixture-image-'+name.encode()
    (root/'assets'/ (name+'.png')).write_bytes(data)
    files.append({'path': 'assets/'+name+'.png', 'kind': 'asset', 'encoding': 'png', 'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()})
    (root/'manifest.json').write_text(json.dumps({'id': name, 'format': 'elysium.source', 'revision': 41, 'scope': {'mode': 'selection', 'collections': ['items','assets','strings']}, 'files': files}))


def item(key, registry, export, nbt=None):
    return {'id': key, 'registry': registry, 'meta': 0, 'name': export+'-text', 'icon': export+'-image', 'nbt': nbt, 'tooltip': [export+'-text']}


class MergeCatalogTests(unittest.TestCase):
    def test_primary_wins_and_only_missing_untagged_identities_fall_back(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            old, new, output = root/'old', root/'new', root/'merged'
            fixture(old, 'old', [item('shared','test:shared','old'), item('fallback','test:lens','old'), item('oldtag','test:oldtag','old', {'type':'compound','value':{}})])
            fixture(new, 'new', [item('shared','test:shared','new'), item('lens-tag','test:lens','new', {'type':'compound','value':{}}), item('added','test:added','new')])
            before={str(path):path.read_bytes() for source in [old,new] for path in source.rglob('*') if path.is_file()}
            report=merge_catalog(new,old,output)
            self.assertEqual(report['total_plain'],3)
            self.assertEqual(report['compatibility_plain'],1)
            manifest,records,_=read_export(output)
            self.assertEqual(records['items']['shared']['name'],'new-text')
            self.assertIsNone(records['items']['fallback']['nbt'])
            self.assertIsNotNone(records['items']['lens-tag']['nbt'])
            self.assertNotIn('oldtag',records['items'])
            self.assertEqual((output/'assets/old.png').read_bytes(),(old/'assets/old.png').read_bytes())
            self.assertEqual(manifest['composition']['fallback_item_ids'],['fallback'])
            for path,data in before.items():self.assertEqual(Path(path).read_bytes(),data)

    def test_rejects_mutating_source_output_and_corrupted_export(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);old,new=root/'old',root/'new'
            fixture(old,'old',[item('a','test:a','old')]);fixture(new,'new',[item('a','test:a','new')])
            with self.assertRaisesRegex(ValueError,'separate'):merge_catalog(new,old,new/'output')
            (new/'assets/new.png').write_bytes(b'corrupt')
            with self.assertRaisesRegex(ValueError,'checksum'):merge_catalog(new,old,root/'output')

    def test_recovers_verified_fallback_images_from_texture_zip(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); old, new = root/'old', root/'new'
            fixture(old, 'old', [item('fallback','test:lens','old')])
            fixture(new, 'new', [item('added','test:added','new')])
            image = (old/'assets/old.png').read_bytes()
            with zipfile.ZipFile(root/'textures.zip','w') as archive:
                archive.writestr('assets/old.png',image)
            (old/'assets/old.png').unlink()
            report = merge_catalog(new, old, root/'merged', root/'textures.zip')
            self.assertEqual(report['compatibility_plain'],1)
            self.assertEqual((root/'merged/assets/old.png').read_bytes(),image)
            with zipfile.ZipFile(root/'corrupt.zip','w') as archive:
                archive.writestr('assets/old.png',b'wrong')
            with self.assertRaisesRegex(ValueError,'checksum'):
                merge_catalog(new,old,root/'rejected',root/'corrupt.zip')


if __name__=='__main__':unittest.main()
