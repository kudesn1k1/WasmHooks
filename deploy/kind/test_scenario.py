import unittest

import scenario


class AcceptanceChecks(unittest.TestCase):
    def test_probe_section_contains_run_id_after_its_name(self):
        wasm = b"fixture"
        marked = scenario.probe_module(wasm, "123456789a")
        header = marked[len(wasm):]
        section_id, size, name_size = header[:3]
        self.assertEqual(section_id, 0)
        self.assertEqual(len(header[2:]), size)
        self.assertEqual(header[3:3 + name_size], b"m3-probe")
        self.assertEqual(header[3 + name_size:], bytes.fromhex("123456789a"))
        self.assertNotEqual(marked, scenario.probe_module(wasm, "abcdefabcd"))

    def test_checkout_validates_amount_even_during_failover(self):
        body = dict(shop_id='merchant-a', customer_id='c-loyal', subtotal=45000, total=40500,
                    discount=dict(percent=10, amount=4500, reason='loyal_customer',
                                  outcome='ok', fallback=False))
        self.assertEqual(scenario.checkout_result('merchant-a', 10, body), ('ok', False))
        body['total'] = 45000
        with self.assertRaises(ValueError):
            scenario.checkout_result('merchant-a', 10, body)
        body['discount'] = dict(percent=0, amount=0, reason=None, outcome='unavailable', fallback=True)
        self.assertEqual(scenario.checkout_result('merchant-a', 10, body), ('unavailable', True))
        body['discount']['amount'] = 1
        with self.assertRaises(ValueError):
            scenario.checkout_result('merchant-a', 10, body)

    def test_removal_moves_only_failed_owners(self):
        before = dict(a='dead', b='live1', c='live2')
        after = dict(a='live1', b='live1', c='live2')
        self.assertEqual(scenario.moved_pairs(before, after, 'dead'), ['a'])
        after['b'] = 'live2'
        with self.assertRaises(ValueError):
            scenario.moved_pairs(before, after, 'dead')

    def test_verifies_kind_provider_and_rejects_unrelated_node(self):
        name = 'wasmhooks-m3-control-plane'
        node = dict(metadata=dict(name=name), spec=dict(providerID=f'kind://docker/wasmhooks-m3/{name}'))
        self.assertEqual(scenario.schedulable_nodes([node]), [name])
        node['spec']['unschedulable'] = True
        self.assertEqual(scenario.schedulable_nodes([node]), [])
        node['spec']['providerID'] = 'aws:///test/production'
        with self.assertRaises(ValueError):
            scenario.schedulable_nodes([node])

    def test_execution_records_prove_routing_to_claimed_owner(self):
        item = dict(msg='invocation', idempotency_key='probe1', tenant_id='shop', hook=scenario.HOOK,
                    module_hash='sha256:abc', outcome='ok', executor_id='pod-a')
        calls = {'probe1': ('shop', 'pod-a', 'sha256:abc')}
        scenario.verify_execution_records(calls, {'pod-a': [item]})
        with self.assertRaises(ValueError):
            scenario.verify_execution_records(calls, {'pod-a': [], 'pod-b': [item]})
        with self.assertRaises(ValueError):
            scenario.verify_execution_records(calls, {'pod-a': [item, item]})

    def test_transition_fallback_fraction_uses_only_checkouts(self):
        samples = [dict(tenant='a', kind='checkout', outcome='unavailable', fallback=True, ms=1),
                   dict(tenant='b', kind='checkout', outcome='ok', fallback=False, ms=10),
                   dict(tenant='c', kind='invoke', outcome='ok', fallback=False, ms=5)]
        summary = scenario.summarize(samples)
        self.assertEqual(summary['checkout_fallback_fraction'], 0.5)
        self.assertEqual(summary['p99_ms'], 10)


if __name__ == '__main__':
    unittest.main()
