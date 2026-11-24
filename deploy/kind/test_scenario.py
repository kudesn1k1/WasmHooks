import unittest
from types import SimpleNamespace
from unittest.mock import patch

import scenario


class AcceptanceChecks(unittest.TestCase):
    def test_invoke_status_and_outcome_in_stable_and_transition_phases(self):
        platform = object.__new__(scenario.Platform)
        platform.args = SimpleNamespace(gateway_url='http://gateway')
        platform.key = 'test'
        platform.bindings = {'t': {'module_hash': 'abc', 'percent': 10}}
        ok = dict(outcome='ok', module_hash='abc', result=dict(discount_percent=10, reason='loyal_customer'))
        cases = [
            (200, ok, True, True),
            (503, dict(outcome='unavailable'), True, False),
            (502, dict(status=502), True, False),
            (200, dict(outcome='unavailable'), False, False),
            (429, dict(outcome='unavailable'), False, False),
            (503, ok, False, False),
            (200, dict(outcome='unknown_result'), False, False),
            (502, dict(status=502, outcome='unavailable'), False, False),
            (429, dict(outcome='quota_exceeded'), False, False),
        ]
        for status, body, transition_ok, stable_ok in cases:
            with patch.object(scenario, 'request', return_value=(status, body)):
                sample = platform.sample('t', 'invoke')
            for phase in ('loss_transition', 'rejoin_transition', 'recovered'):
                with self.subTest(status=status, body=body, phase=phase):
                    record = {'samples': [sample], 'load': {'requests': 1}}
                    load = scenario.Load(platform, None, {'phases': {phase: record}})
                    load.scheduled[phase] = 1
                    accepted = transition_ok if phase in scenario.TRANSITIONS else stable_ok
                    if accepted:
                        load.check_results()
                    else:
                        with self.assertRaises(ValueError):
                            load.check_results()

    def test_replacement_without_original_pairs_needs_a_logged_invocation(self):
        platform = object.__new__(scenario.Platform)
        platform.run_id = '000000028b'
        platform.args = SimpleNamespace(gateway_url='http://gateway', convergence_seconds=2, series_rounds=1)
        platform.key = 'test'
        platform.bindings = {'original': {'module_hash': 'hash', 'percent': 11}}
        previous = {'original': 'a'}
        pods = {'a': {'name': 'survivor'}, 'b': {'name': 'replacement'}}
        load = scenario.Load(platform, None, {'phases': {'recovered': {'samples': []}}})
        load.phase = 'recovered'
        registered = dict(platform.bindings)
        events = []

        def diagnostic(tenant):
            owner = original_owner if tenant == 'original' else ('b' if tenant.endswith('-1') else 'a')
            return dict(members=['a', 'b'], owners=[owner])

        def register(tenant, binary, percent):
            registered[tenant] = dict(module_hash='hash', percent=percent)
            return registered[tenant]

        def invoke(method, url, token, body, statuses):
            tenant = body['tenant_id']
            binding = registered[tenant]
            pod = pods[original_owner]['name'] if tenant == 'original' else probe_pod
            events.append(dict(msg='invocation', outcome='ok', hook=scenario.HOOK, executor_id=pod,
                               idempotency_key=body['idempotency_key'], tenant_id=tenant,
                               module_hash=binding['module_hash']))
            return 200, dict(outcome='ok', module_hash=binding['module_hash'],
                             result=dict(discount_percent=binding['percent'], reason='loyal_customer'))

        def logs(pod):
            return [e for e in events if e['executor_id'] == pod]

        with patch.object(platform, 'diagnostic', diagnostic), patch.object(
                platform, 'register_binding', register
        ), patch.object(scenario, 'request', invoke), patch.object(scenario, 'pod_logs', logs):
            original_owner = 'b'
            recovered = load.verify_recovered(previous, pods, 'b')
            self.assertEqual(recovered['moved_pairs'], ['original'])
            self.assertEqual(set(registered), {'original'})

            original_owner = 'a'
            probe_pod = 'survivor'
            with self.assertRaisesRegex(ValueError, 'execution of'):
                load.verify_recovered(previous, pods, 'b')
            probe_pod = 'replacement'
            recovered = load.verify_recovered(previous, pods, 'b')
        proof = recovered['replacement_probe']
        self.assertIn(proof['call']['key'], [e['idempotency_key'] for e in logs('replacement')
                                            if e['tenant_id'] == proof['tenant']])
        self.assertEqual(recovered['owners'], previous)
        self.assertEqual(recovered['moved_pairs'], [])
        self.assertEqual(set(platform.bindings), {'original'})

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
