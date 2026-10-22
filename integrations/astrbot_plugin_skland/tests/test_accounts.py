import asyncio
import concurrent.futures
import json
import pathlib
import sqlite3
import tempfile
import threading
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from test_security import Event, Store, Service, identity, now


class Provider:
    def __init__(self):
        self.tokens = {'ONE': ('account-one', 'role-one'), 'ROTATED': ('account-one', 'role-one'),
                       'TWO': ('account-two', 'role-two')}
        self.current = None
        self.writes = 0

    async def get_authorization(self, token):
        self.current = self.tokens[token]
        return 'grant'

    async def get_credential(self, grant):
        return SimpleNamespace(user_id=self.current[0])

    async def get_binding_list(self, cred):
        return [SimpleNamespace(app_code='arknights', game_name='明日方舟', nickname='same nickname',
                                game_id=1, uid=self.current[1], roles=[])]

    async def get_attendance_status(self, cred, binding, role=None):
        return True

    async def sign_arknights(self, cred, binding):
        self.writes += 1
        raise AssertionError('Already completed accounts must not be submitted again')


class AccountRulesTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.store = Store(self.temp.name)
        self.provider = Provider()
        self.service = Service(self.store, self.provider, {'max_users': 10, 'auto_sign_delay': 0})
        self.addCleanup(self.temp.cleanup)
        self.addCleanup(self.store.db.close)

    async def candidate(self, event, token):
        request = self.service.request(event)
        cookie, _ = self.service.browser(request)
        code = self.service.public(self.service.session(cookie))['command'].split()[1]
        self.service.confirm(event, code)
        await self.service.verify(cookie, token)
        return cookie

    async def bind(self, event=None, token='ONE', auto=None):
        event = event or Event('first-owner')
        cookie = await self.candidate(event, token)
        await self.service.commit(cookie, auto)
        return cookie

    async def test_duplicate_token_different_qq_is_rejected_privately(self):
        await self.bind()
        with self.assertRaises(ValueError) as error:
            await self.candidate(Event('second-owner'), 'ONE')
        self.assertIn('已绑定', str(error.exception))
        self.assertNotIn('first-owner', str(error.exception))
        self.assertEqual(len(self.store.accounts()), 1)

    async def test_rotated_token_and_changed_roles_still_match_platform_account(self):
        await self.bind()
        self.provider.tokens['ROTATED'] = ('account-one', 'new-role')
        with self.assertRaises(ValueError):
            await self.candidate(Event('second-owner', instance='other-bot'), 'ROTATED')

    async def test_overlapping_game_role_is_rejected_without_platform_id_match(self):
        await self.bind()
        self.provider.tokens['TWO'] = ('account-two', 'role-one')
        with self.assertRaises(ValueError):
            await self.candidate(Event('second-owner'), 'TWO')

    async def test_same_nickname_is_not_account_identity(self):
        await self.bind()
        await self.bind(Event('second-owner'), 'TWO')
        self.assertEqual(len(self.store.accounts()), 2)

    async def test_refresh_preserves_history_opt_out_and_capacity(self):
        cookie = await self.bind(auto=False)
        uid = identity(Event('first-owner'))
        self.store.record(uid, 'arknights:1:role-one', now().date().isoformat(), 'already', 'verified', 1)
        old_version = self.store.account(uid)[0]
        refresh = await self.candidate(Event('first-owner'), 'ROTATED')
        state = self.service.public(self.service.session(refresh))
        self.assertEqual(state['binding_mode'], 'refresh')
        self.assertFalse(state['auto_sign'])
        await self.service.commit(refresh)
        self.assertEqual(len(self.store.accounts()), 1)
        self.assertFalse(self.store.account(uid)[1]['auto'])
        self.assertEqual(self.store.account(uid)[1]['token'], 'ROTATED')
        self.assertEqual(self.store.results(uid, now().date().isoformat())[0]['message'], 'verified')
        self.assertNotEqual(self.store.account(uid)[0], old_version)
        with self.assertRaises(ValueError):
            await self.service.commit(cookie)

    async def test_two_verified_candidates_cannot_both_commit(self):
        first, second = Event('first-owner'), Event('second-owner')
        a = await self.candidate(first, 'ONE')
        b = await self.candidate(second, 'ROTATED')
        await self.service.commit(a)
        with self.assertRaises(ValueError):
            await self.service.commit(b)
        self.assertEqual(len(self.store.accounts()), 1)
        self.assertFalse(self.service.public(self.service.session(b))['complete'])

    async def test_unbinding_releases_claims_for_explicit_rebinding(self):
        await self.bind()
        uid = identity(Event('first-owner'))
        self.assertTrue(self.store.remove(uid, self.store.account(uid)[0]))
        await self.bind(Event('second-owner'), 'ROTATED')
        self.assertEqual(len(self.store.accounts()), 1)

    async def test_replacing_own_account_releases_old_claims_and_old_results(self):
        await self.bind()
        uid = identity(Event('first-owner'))
        self.store.record(uid, 'arknights:1:role-one', now().date().isoformat(), 'already', 'old')
        cookie = await self.candidate(Event('first-owner'), 'TWO')
        self.assertEqual(self.service.public(self.service.session(cookie))['binding_mode'], 'replace')
        await self.service.commit(cookie)
        self.assertFalse(self.store.results(uid, now().date().isoformat()))
        await self.bind(Event('second-owner'), 'ONE')

    async def test_transaction_rollback_preserves_claims_and_credentials(self):
        await self.bind()
        uid = identity(Event('first-owner'))
        version, data = self.store.account(uid)
        before = [tuple(r) for r in self.store.db.execute('SELECT * FROM account_claims ORDER BY claim,identity')]
        def fail():
            raise RuntimeError('fixture rollback')
        with self.assertRaises(RuntimeError):
            self.store.put(uid, {'token': 'TWO'}, expected=version, finalize=fail)
        self.assertEqual(self.store.account(uid), (version, data))
        self.assertEqual(before, [tuple(r) for r in self.store.db.execute('SELECT * FROM account_claims ORDER BY claim,identity')])

    async def test_legacy_collisions_are_preserved_blocked_and_can_be_disabled(self):
        await self.bind()
        first, second = identity(Event('first-owner')), identity(Event('second-owner'))
        self.store.db.execute('INSERT INTO accounts VALUES(?,?,?)',
                              (second, 'legacy-version', self.store.seal({'token': 'ROTATED', 'role_keys': ['arknights:1:role-one'], 'auto': True})))
        self.store.rebuild_claims()
        self.assertEqual(len(self.store.accounts()), 2)
        self.assertTrue(self.store.conflicted(first))
        self.assertTrue(self.store.conflicted(second))
        with self.assertRaises(ValueError):
            await self.service.sign(first)
        version, data = self.store.account(second)
        data['auto'] = False
        self.assertTrue(self.store.update(second, version, data))
        self.assertFalse(self.store.account(second)[1]['auto'])
        self.assertEqual(self.provider.writes, 0)

    async def test_claims_are_stable_private_and_rebuilt_after_restart(self):
        await self.bind()
        expected = [tuple(r) for r in self.store.db.execute('SELECT * FROM account_claims ORDER BY claim,identity')]
        self.assertTrue(all(len(r[0]) == 64 and 'account-one' not in r[0] and 'role-one' not in r[0] for r in expected))
        other = Store(self.temp.name)
        try:
            self.assertEqual(expected, [tuple(r) for r in other.db.execute('SELECT * FROM account_claims ORDER BY claim,identity')])
        finally:
            other.db.close()

    async def test_new_accounts_default_to_auto_and_explicit_global_off_wins(self):
        cookie = await self.bind()
        uid = identity(Event('first-owner'))
        self.assertTrue(self.store.account(uid)[1]['auto'])
        self.assertTrue(self.service.public(self.service.session(cookie))['schedule']['enabled'])
        self.service.config['auto_sign_enabled'] = False
        self.assertFalse(await self.service.due())
        version, data = self.store.account(uid)
        data['auto'] = False
        self.store.update(uid, version, data)
        self.service.config.pop('auto_sign_enabled')
        self.assertFalse(await self.service.due())

    async def test_claim_expansion_cannot_take_another_accounts_role(self):
        await self.bind()
        await self.bind(Event('second-owner'), 'TWO')
        uid = identity(Event('first-owner'))
        version, original = self.store.account(uid)
        updated = dict(original, role_keys=['arknights:1:role-two'])
        with self.assertRaises(ValueError):
            self.store.update(uid, version, updated)
        self.assertEqual(self.store.account(uid), (version, original))

    async def test_legacy_game_role_blocks_new_token_before_commit(self):
        uid = identity(Event('first-owner'))
        self.store.put(uid, {'token': 'OLD_TOKEN', 'role_keys': ['arknights:1:role-one']})
        with self.assertRaises(ValueError):
            await self.candidate(Event('second-owner'), 'ROTATED')

    async def test_unverified_legacy_binding_cannot_auto_start(self):
        await self.bind()
        uid = identity(Event('first-owner'))
        version, data = self.store.account(uid)
        data['dedupe_pending'] = True
        self.store.update(uid, version, data)
        with self.assertRaises(ValueError):
            await self.service.sign(uid)
        self.assertFalse(await self.service.due())
        self.assertEqual(self.provider.writes, 0)

    async def test_legacy_import_retains_duplicates_without_allowing_new_takeovers(self):
        self.store.put('legacy-first', {'token': 'OLD_TOKEN'}, legacy_import=True)
        self.store.put('legacy-second', {'token': 'OLD_TOKEN'}, legacy_import=True)
        self.assertEqual(len(self.store.accounts()), 2)
        self.assertTrue(self.store.conflicted('legacy-first'))
        with self.assertRaises(ValueError):
            self.store.put('new-user', {'token': 'OLD_TOKEN'})
        with self.assertRaises(ValueError):
            self.store.put('legacy-first', {'token': 'replacement'}, legacy_import=True)

    async def test_old_inflight_confirmation_must_reverify_claims(self):
        cookie = await self.candidate(Event('first-owner'), 'ONE')
        session = self.service.session(cookie)
        pending = self.store.unseal(session['pending'])
        pending.pop('claim_keys')
        self.store.db.execute('UPDATE sessions SET pending=? WHERE id=?', (self.store.seal(pending), session['id']))
        with self.assertRaisesRegex(ValueError, '重新验证'):
            await self.service.commit(cookie)
        self.assertEqual(len(self.store.accounts()), 0)


class ConcurrentClaimsTests(unittest.TestCase):
    def test_separate_database_connections_serialize_duplicate_commit(self):
        with tempfile.TemporaryDirectory() as root:
            initial = Store(root)
            initial.db.close()
            barrier = threading.Barrier(2)
            def commit(uid):
                store = Store(root)
                try:
                    barrier.wait(timeout=5)
                    try:
                        store.put(uid, {'token': 'SAME_TOKEN'}, expected='')
                        return 'created'
                    except ValueError:
                        return 'duplicate'
                finally:
                    store.db.close()
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                results = list(pool.map(commit, ('first', 'second')))
            self.assertCountEqual(results, ['created', 'duplicate'])


if __name__ == '__main__':
    unittest.main()
