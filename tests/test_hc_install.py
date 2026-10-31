import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('install_hc_test', ROOT / 'scripts/install_runtime.py')
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)

class HCInstallTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.pkg = self.root / 'pkg'
        self.pkg.mkdir()
        binary = self.pkg / 'hc'
        binary.write_text('''#!/bin/sh
case "$1" in
 --version) echo 'hc 0.1.14';;
 --skill) echo 'matched HC manual';;
 schema) echo '{"openrpc":"1.3.2","methods":[]}';;
esac
''')
        binary.chmod(0o755)
        self.archive = self.root / 'hc.tar.gz'
        with tarfile.open(self.archive, 'w:gz') as bundle:
            bundle.add(binary, arcname='hc')
        self.manifest = {'version':'0.1.14','generation':1,'targets':{'macos-aarch64':{'url':'https://releases.herdr.dev/core/0.1.14/herdr-core-macos-aarch64.tar.gz','sha256':hashlib.sha256(self.archive.read_bytes()).hexdigest()}}}
        self.urls=[]
    def fetch(self,url,path):
        self.urls.append(url)
        if url.endswith('manifest.json'):
            path.write_text(json.dumps(self.manifest))
        else:
            shutil.copyfile(self.archive,path)
    def test_exact_pinned_runtime_and_matched_docs_are_staged_once(self):
        target=self.root/'runtime'
        result=runtime.install_hc(target,fetch=self.fetch,manifest=self.manifest,platform_name='darwin-arm64')
        self.assertTrue(result['installed'])
        link=target/'.local/bin/hc'
        self.assertTrue(link.is_file())
        self.assertFalse(os.path.isabs(os.readlink(link)))
        self.assertEqual((target/'.local/share/hc/manual.md').read_text(),'matched HC manual\n')
        self.assertEqual(json.loads((target/'.local/share/hc/openrpc.json').read_text())['openrpc'],'1.3.2')
        self.assertEqual(json.loads((target/'.local/share/hc/manifest.json').read_text()),self.manifest)
        before=list(self.urls)
        again=runtime.install_hc(target,fetch=self.fetch,manifest=self.manifest,platform_name='darwin-arm64')
        self.assertFalse(again['created'])
        self.assertEqual(self.urls,before)
    def test_checksum_mismatch_leaves_no_binary_or_destination(self):
        target=self.root/'runtime'
        self.manifest['targets']['macos-aarch64']['sha256']='0'*64
        with self.assertRaisesRegex(runtime.InstallError,'checksum mismatch'):
            runtime.install_hc(target,fetch=self.fetch,manifest=self.manifest,platform_name='darwin-arm64')
        self.assertFalse((target/'.local/bin/hc').exists())
        self.assertFalse((target/'.deps/hc/0.1.14-darwin-arm64').exists())
    def test_mutated_versioned_manifest_is_refused(self):
        locked=json.loads(json.dumps(self.manifest))
        self.manifest['version']='0.1.15'
        with self.assertRaisesRegex(runtime.InstallError,'manifest'):
            runtime.install_hc(self.root/'runtime',fetch=self.fetch,manifest=locked,platform_name='darwin-arm64')
    def test_preexisting_custom_binary_is_not_replaced(self):
        target=self.root/'runtime'
        link=target/'.local/bin/hc'
        link.parent.mkdir(parents=True)
        link.write_text('custom')
        with self.assertRaises(runtime.InstallError):
            runtime.install_hc(target,fetch=self.fetch,manifest=self.manifest,platform_name='darwin-arm64')
        self.assertEqual(link.read_text(),'custom')
    def test_no_download_setting_is_honored(self):
        old=os.environ.get('SUM_HC_NO_DOWNLOAD')
        os.environ['SUM_HC_NO_DOWNLOAD']='1'
        self.addCleanup(lambda: os.environ.pop('SUM_HC_NO_DOWNLOAD',None) if old is None else os.environ.__setitem__('SUM_HC_NO_DOWNLOAD',old))
        with self.assertRaisesRegex(runtime.InstallError,'disabled'):
            runtime.install_hc(self.root/'runtime',manifest=self.manifest,platform_name='darwin-arm64')

    def test_cached_release_metadata_must_identify_same_artifact(self):
        for field,bad in [('version','0.1.13'),('generation',2),('platform','linux-amd64'),('source_commit','0'*40),('binary','../../outside')]:
            with self.subTest(field=field):
                target=self.root/('runtime-'+field)
                runtime.install_hc(target,fetch=self.fetch,manifest=self.manifest,platform_name='darwin-arm64')
                receipt=target/'.deps/hc/0.1.14-darwin-arm64/contract/provenance.json'
                value=json.loads(receipt.read_text());value[field]=bad;receipt.write_text(json.dumps(value))
                with self.assertRaises(runtime.InstallError):
                    runtime.install_hc(target,fetch=self.fetch,manifest=self.manifest,platform_name='darwin-arm64')
    def test_cached_manifest_must_match_locked_manifest(self):
        target=self.root/'cached'
        runtime.install_hc(target,fetch=self.fetch,manifest=self.manifest,platform_name='darwin-arm64')
        saved=target/'.deps/hc/0.1.14-darwin-arm64/contract/manifest.json'
        saved.write_text('{}')
        with self.assertRaises(runtime.InstallError):
            runtime.install_hc(target,fetch=self.fetch,manifest=self.manifest,platform_name='darwin-arm64')

if __name__=='__main__':unittest.main()
