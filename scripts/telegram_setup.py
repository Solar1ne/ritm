#!/usr/bin/env python3
"""Configure only the bot's public product metadata; never sends messages."""
import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


def mini_url(origin):
    parsed = urllib.parse.urlsplit(origin)
    if (parsed.scheme != 'https' or not parsed.hostname or parsed.username or
            parsed.password or parsed.path not in ('', '/') or parsed.query or parsed.fragment):
        raise ValueError('PUBLIC_URL must be an HTTPS origin')
    return origin.rstrip('/') + '/mini'


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class BotAPI:
    def __init__(self, token):
        if not token or any(char in token for char in '/?#\r\n '):
            raise ValueError('invalid TELEGRAM_BOT_TOKEN')
        self._token = token
        self._opener = urllib.request.build_opener(NoRedirect)

    def call(self, method, payload=None):
        request = urllib.request.Request(
            'https://api.telegram.org/bot' + self._token + '/' + method,
            data=json.dumps(payload or {}).encode(),
            headers={'Content-Type': 'application/json'}, method='POST')
        try:
            with self._opener.open(request, timeout=15) as response:
                raw = response.read(1024 * 1024 + 1)
            if len(raw) > 1024 * 1024:
                raise ValueError('Telegram response exceeds limit')
            result = json.loads(raw)
            if not result.get('ok'):
                raise ValueError('Telegram API rejected request')
            return result['result']
        except (urllib.error.URLError, OSError, ValueError, KeyError):
            # urllib errors include the credential-bearing URL. Never expose them.
            raise RuntimeError('Telegram API request failed') from None


def configure(api, origin, apply=False):
    mini = mini_url(origin)
    me = api.call('getMe')
    if not me.get('is_bot') or not me.get('username'):
        raise ValueError('Telegram bot identity is invalid')
    hook = api.call('getWebhookInfo')
    if apply:
        api.call('setMyCommands', {'commands': [
            {'command': 'start', 'description': 'Открыть Ритм'},
            {'command': 'help', 'description': 'Как пользоваться'},
            {'command': 'privacy', 'description': 'О данных и приватности'}]})
        api.call('setChatMenuButton', {'menu_button': {
            'type': 'web_app', 'text': 'Мой ритм', 'web_app': {'url': mini}}})
        api.call('setMyDescription', {'description':
            'Ритм — план тренировок и дневник самочувствия. Откройте приложение кнопкой «Мой ритм».'})
        api.call('setMyShortDescription', {'short_description':
            'План тренировок и дневник самочувствия.'})
    # Public metadata can become visible slightly after the successful write.
    # Recheck reads only: never repeat the mutations or consume bot updates.
    verified = False
    for attempt in range(4 if apply else 1):
        if attempt:
            time.sleep(0.5 * attempt)
        menu = api.call('getChatMenuButton')
        commands = api.call('getMyCommands')
        verified = (menu.get('type') == 'web_app' and menu.get('web_app', {}).get('url') == mini
                    and {c.get('command') for c in commands} == {'start', 'help', 'privacy'})
        if verified:
            break
    return {'username': me['username'], 'mini_url': mini, 'configured': verified,
            'webhook_present': bool(hook.get('url'))}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument('--check', action='store_true')
    mode.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    try:
        # Validate the public origin before any network call.
        origin = os.environ.get('PUBLIC_URL', '')
        mini_url(origin)
        result = configure(BotAPI(os.environ.get('TELEGRAM_BOT_TOKEN', '')), origin, args.apply)
        print(json.dumps(result, ensure_ascii=False))
        return 0 if result['configured'] else 1
    except (ValueError, RuntimeError) as error:
        print(str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
