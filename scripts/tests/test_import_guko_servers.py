import importlib.util
import pathlib
import sys
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / 'import-guko-servers.py'
spec = importlib.util.spec_from_file_location('import_guko_servers', SCRIPT)
mod = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = mod
spec.loader.exec_module(mod)


class ImportGukoServersTest(unittest.TestCase):
    def test_desired_nodes_from_guko_normalizes_inventory(self):
        nodes = mod.desired_nodes_from_guko([
            {'name': 'Example Edge HK', 'host': '198.51.100.20', 'country': 'hk'},
            {'name': 'Example Compute', 'host': '192.0.2.40', 'ipv6': '2607:f5b4:88:106:1c00:f2ff:fe00:5a4', 'country': 'us'},
            {'name': 'broken', 'host': 'not-an-ip', 'country': 'zz'},
        ])
        self.assertEqual([n.id for n in nodes], ['example-edge-hk', 'example-compute'])
        self.assertEqual(nodes[0].country_code, 'HK')
        self.assertEqual(nodes[0].public_ipv4, '198.51.100.20')
        self.assertEqual(nodes[0].display_order, 10)
        self.assertEqual(nodes[1].public_ipv6, '2607:f5b4:88:106:1c00:f2ff:fe00:5a4')
        self.assertEqual(nodes[1].country_code, 'US')

    def test_patch_payload_omits_missing_ipv6_so_existing_value_is_preserved(self):
        node = mod.DesiredNode(
            id='example-node-a',
            display_name='Example Node A',
            country_code='HK',
            display_order=30,
            public_ipv4='198.51.100.144',
            public_ipv6='',
        )
        patch = node.patch_payload({
            'id': 'example-node-a',
            'display_name': 'Example Node A',
            'country_code': 'HK',
            'display_order': 20,
            'public_ipv4': '198.51.100.144',
            'public_ipv6': '2401:b60:e011::2',
        })
        self.assertEqual(patch, {'display_order': 30})

    def test_plan_changes_separates_creates_and_patches(self):
        desired = [
            mod.DesiredNode('example-node-a', 'Example Node A', 'HK', 30, '198.51.100.144', ''),
            mod.DesiredNode('ccs', 'CCS', 'US', 100, '203.0.113.26', ''),
        ]
        creates, patches = mod.plan_changes(desired, [{'id': 'example-node-a', 'display_name': 'Example Node A', 'country_code': 'HK', 'display_order': 1, 'public_ipv4': '198.51.100.144'}])
        self.assertEqual([n.id for n in creates], ['ccs'])
        self.assertEqual([(n.id, p) for n, p in patches], [('example-node-a', {'display_order': 30})])


if __name__ == '__main__':
    unittest.main()
