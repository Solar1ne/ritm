import unittest
from unittest.mock import patch
import urllib.error
import telegram_setup as setup


class SetupTests(unittest.TestCase):
    def test_origins(self):
        self.assertEqual(setup.mini_url('https://rhythm.example/'), 'https://rhythm.example/mini')
        for value in ('http://example.com', 'https://user:secret@example.com',
                      'https://example.com/path', 'https://example.com?token=secret', ''):
            with self.subTest(value=value), self.assertRaises(ValueError):
                setup.mini_url(value)

    def test_check_is_read_only_and_apply_verifies(self):
        class API:
            def __init__(self):
                self.calls = []
            def call(self, method, payload=None):
                self.calls.append(method)
                return {'getMe': {'is_bot': True, 'username': 'rhythm_bot'},
                        'getWebhookInfo': {'url': 'https://existing.example'},
                        'getChatMenuButton': {'type': 'web_app', 'web_app': {'url': 'https://rhythm.example/mini'}},
                        'getMyCommands': [{'command': c} for c in ('start', 'help', 'privacy')]}.get(method, True)
        api = API()
        self.assertTrue(setup.configure(api, 'https://rhythm.example')['configured'])
        self.assertFalse(any(name.startswith('set') for name in api.calls))
        self.assertTrue(setup.configure(api, 'https://rhythm.example', True)['webhook_present'])
        self.assertIn('setChatMenuButton', api.calls)
        self.assertNotIn('deleteWebhook', api.calls)
        self.assertNotIn('getUpdates', api.calls)

    def test_error_redacts_credential(self):
        api = setup.BotAPI('123:SECRET')
        with patch.object(api._opener, 'open', side_effect=urllib.error.URLError('https://api.telegram.org/bot123:SECRET/getMe')):
            with self.assertRaises(RuntimeError) as raised:
                api.call('getMe')
        self.assertNotIn('SECRET', str(raised.exception))

    def test_metadata_visibility_is_bounded_and_only_reads_are_retried(self):
        class API:
            def __init__(self):
                self.calls, self.reads = [], 0
            def call(self, method, payload=None):
                self.calls.append(method)
                if method == 'getMe':
                    return {'is_bot': True, 'username': 'rhythm_bot'}
                if method == 'getWebhookInfo':
                    return {'url': ''}
                if method == 'getChatMenuButton':
                    self.reads += 1
                    return {'type': 'web_app', 'web_app': {'url': 'https://rhythm.example/mini'}}
                if method == 'getMyCommands':
                    return [] if self.reads < 3 else [{'command': c} for c in ('start', 'help', 'privacy')]
                return True
        api = API()
        with patch('telegram_setup.time.sleep') as sleep:
            self.assertTrue(setup.configure(api, 'https://rhythm.example', True)['configured'])
        self.assertEqual(sleep.call_count, 2)
        self.assertEqual(api.calls.count('setChatMenuButton'), 1)
        api = API()
        with patch('telegram_setup.time.sleep') as sleep:
            self.assertFalse(setup.configure(api, 'https://rhythm.example')['configured'])
        self.assertEqual(api.reads, 1)
        sleep.assert_not_called()


if __name__ == '__main__':
    unittest.main()
