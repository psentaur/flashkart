"""Stream Go JSON test events to an ignored, run-specific evidence artifact."""
import datetime,json,subprocess
from dev import ROOT

def run_report(arguments,environment,label):
    stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
    folder=ROOT/'.cache/evidence'/stamp;folder.mkdir(parents=True)
    path=folder/(label+'.jsonl')
    with path.open('w') as report:
        process=subprocess.Popen(['go','test','-json',*arguments],cwd=ROOT/'tests',env=environment,stdout=subprocess.PIPE,text=True)
        for line in process.stdout:
            report.write(line)
            try:event=json.loads(line)
            except ValueError:
                print(line,end='');continue
            if event.get('Action')=='output':print(event['Output'],end='')
        code=process.wait()
    print('Test evidence: '+str(path.relative_to(ROOT)))
    return code
