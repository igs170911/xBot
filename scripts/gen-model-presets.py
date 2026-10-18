# 把 LazyBoy server/hermes_providers.json 轉成 internal/proxy/presets.json（Model Proxy 的內建供應商範本，E11）。
# 用法：python3 scripts/gen-model-presets.py <hermes_providers.json> internal/proxy/presets.json
import json, sys
src, dst = sys.argv[1], sys.argv[2]
d = json.load(open(src))
m = {'chat_completions': 'chat_completions', 'responses': 'codex_responses', 'messages': 'anthropic_messages'}
out = []
for p in d:
    if not p.get('base_url'):
        continue
    base = m[p.get('protocol') or 'chat_completions']
    out.append({
        'id': p['id'], 'label': p['label'], 'base_url': p['base_url'], 'transport': base,
        'signup_url': p.get('signup_url', ''), 'local': bool(p.get('local')), 'models': p.get('models', []),
        'model_transports': {k: m[v['protocol']] for k, v in (p.get('model_info') or {}).items()
                             if v.get('protocol') in m and m[v['protocol']] != base},
    })
# XBot additions: OpenCode needs a stable x-opencode-session per conversation (https://opencode.ai/docs/go/);
# the user's cheap default for OpenCode Go.
for o in out:
    if o['id'] in ('opencode-go', 'opencode-zen'):
        o['session_header'] = 'x-opencode-session'
    if o['id'] == 'opencode-go':
        o['default_model'] = 'deepseek-v4-flash'
        if 'deepseek-v4-flash' not in o['models']:
            o['models'].insert(0, 'deepseek-v4-flash')
json.dump(out, open(dst, 'w'), ensure_ascii=False, indent=1)
print(len(out), sum(1 for o in out if o['model_transports']))
