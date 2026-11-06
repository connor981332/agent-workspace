"""Test driver over the attachment record functions; the tool itself calls them directly."""
import json
from pathlib import Path
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'src'))
from agent_workspace import attachments as a


def end(state, harness, session, generation):
    path = a.path_for(state, harness, session)
    data = a.load(path)
    # A delayed end signal from an earlier generation must not clear a newer reservation.
    if data and data['generation'] == generation and data['state'] != 'Ended':
        data.update(state='Ended', ended_reason='harness end signal', ended_at=int(time.time()))
        a.save(path, data)


def main(argv):
    cmd, args = argv[1], argv[2:]
    if cmd == 'begin':
        a.begin(*args)
    elif cmd == 'set':
        a.cmd_set(*args)
    elif cmd == 'join':
        a.cmd_join(*args)
    elif cmd == 'revive':
        lane = a.revive(*args)
        if lane is None:
            sys.exit(1)
        print(lane)
    elif cmd == 'end':
        end(*args)
    elif cmd == 'lookup':
        path = a.path_for(args[0], args[1], args[2])
        data = a.load(path)
        if data and a.refreshed(path, data)['state'] in a.PROTECTED:
            print(json.dumps(data))
        else:
            sys.exit(1)
    elif cmd == 'protects':
        found = a.protecting(args[0], args[1])
        if not found:
            sys.exit(1)
        data = found[1]
        print(f"lane {args[1]} is held by a {data.get('harness', 'unknown')} attachment ({data['state']})")
    elif cmd == 'list':
        for path, data in a.records(args[0]):
            if len(args) > 1 and str(data['lane']) != args[1]:
                continue
            pids = ','.join(str(p['pid']) for p in a.bound(data)) or '-'
            print(f"{data['lane']}\t{data['state']}\t{data.get('harness', '?')}\tpid={pids}\t{data.get('session', path.name)}")
    elif cmd == 'reconcile':
        for path, data in a.records(args[0]):
            if str(data['lane']) == args[1] and data['state'] in a.PROTECTED:
                data.update(state='Ended', ended_reason='owner reconciliation', ended_at=int(time.time()))
                a.save(path, data)
    elif cmd == 'route':
        state, lanes = args[:2]
        at_start = args[2:] == ['start']
        session = json.load(sys.stdin).get('session_id', '')
        for harness in a.HARNESSES:
            path = a.path_for(state, harness, session)
            data = a.load(path)
            if data and a.refreshed(path, data)['state'] in a.PROTECTED:
                a.adopt(path, data)
                break
            if data and at_start and a.revive_locked(state, harness, session, lanes):
                data = a.load(path)
                break
        else:
            sys.exit(1)
        lane = Path(lanes) / str(int(data['lane']))
        generation = (Path(state) / 'lanes' / str(int(data['lane'])) / 'generation').read_text().strip()
        if generation != data['generation'] or not (lane / '.lane').is_file():
            sys.exit(1)
        print(lane)
    elif cmd == 'path':
        print(a.path_for(*args))
    elif cmd == 'field':
        print(json.load(sys.stdin)[args[0]])
    else:
        sys.exit('unknown command')


if __name__ == '__main__':
    main(sys.argv)
