#!/usr/bin/env python3
"""Run only local dependency containers and Mukthish's Phase 1 binaries."""
from __future__ import annotations
import argparse,json,os,re,secrets,shlex,shutil,subprocess,sys,signal,time
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]

def load_env():
    path=ROOT/".env"
    if not path.exists():raise SystemExit("Run make dev-init first")
    result=os.environ.copy()
    for line in path.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#'):continue
        key,value=line.split('=',1)
        if not re.fullmatch(r'[A-Z][A-Z0-9_]*',key):raise SystemExit("Invalid .env key")
        result.setdefault(key,value)
    return result

def initialize():
    path=ROOT/".env"
    if path.exists():raise SystemExit(".env already exists; preserve its credentials/volume configuration")
    values={}
    for line in (ROOT/"dev/lead.env.example").read_text().splitlines():
        if line.strip() and not line.lstrip().startswith('#'):
            k,v=line.split('=',1);values[k]=v
    for key in ('POSTGRES_ADMIN_PASSWORD','ADMISSION_DB_PASSWORD','FLASH_SALE_DB_PASSWORD','ORDER_DB_PASSWORD'):values[key]=secrets.token_hex(24)
    values['RUN_ID']='local-'+secrets.token_hex(4)
    for prefix,user,database in [('ADMISSION','admission','admission'),('FLASH_SALE','flash_sale','flash_sale'),('ORDER','order_service','order')]:
        values[prefix+'_DATABASE_URL']=f"postgres://{user}:{values[prefix+'_DB_PASSWORD']}@127.0.0.1:{values['POSTGRES_PORT']}/flashkart_{database}?sslmode=disable"
    descriptor=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(descriptor,'w') as handle:handle.write('\n'.join(k+'='+v for k,v in values.items())+'\n')
    print("Created ignored .env with independent random local passwords; no secrets printed")

def compose(arguments):
    env=load_env();env['COMPOSE_DISABLE_ENV_FILE']='true'
    if not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,48}',env['RUN_ID']):raise SystemExit("RUN_ID must be a short lowercase project identifier")
    explicit=env.get('COMPOSE_COMMAND')
    if explicit:command=shlex.split(explicit)
    elif (ROOT/'.tools/bin/docker-compose').exists():command=[str(ROOT/'.tools/bin/docker-compose')]
    elif shutil.which('docker'):command=['docker','compose']
    else:raise SystemExit('Run make bootstrap or configure COMPOSE_COMMAND')
    if arguments[0]!='config' and not env.get('DOCKER_HOST') and not shutil.which('docker'):
        socket=Path(env.get('XDG_RUNTIME_DIR',f'/run/user/{os.getuid()}'))/'podman/podman.sock'
        if not socket.exists():raise SystemExit('Start your Podman API socket or supply DOCKER_HOST')
        env['DOCKER_HOST']='unix://'+str(socket)
    return subprocess.run(command+['--project-directory',str(ROOT/'dev'),'--file',str(ROOT/'dev/compose.yaml'),*arguments],env=env,check=True)

def application_environment(component):
    values=load_env();result=values.copy()
    database_keys=('ADMISSION_DATABASE_URL','FLASH_SALE_DATABASE_URL','ORDER_DATABASE_URL')
    for key in (*database_keys,'POSTGRES_ADMIN_PASSWORD','ADMISSION_DB_PASSWORD','FLASH_SALE_DB_PASSWORD','ORDER_DB_PASSWORD','JWT_PRIVATE_KEY_FILE','SEEDED_TOKENS_FILE'):
        result.pop(key,None)
    own={'admission':'ADMISSION_DATABASE_URL','flash-sale':'FLASH_SALE_DATABASE_URL','order':'ORDER_DATABASE_URL'}.get(component)
    if own and own in values:result[own]=values[own]
    return result

def run_component(component):
    paths={'admission':'services/admission','flash-sale':'services/flash-sale','order':'services/order','analytics':'services/analytics','gateway':'gateway'}
    if component not in paths:raise SystemExit('Choose one of '+', '.join(paths))
    env=application_environment(component);env['GOTOOLCHAIN']='go'+json.loads((ROOT/'dev/tool-versions.json').read_text())['go']
    target=ROOT/'.cache/bin'/component;target.parent.mkdir(parents=True,exist_ok=True)
    subprocess.run(['go','build','-buildvcs=false','-o',str(target),'./cmd/server'],cwd=ROOT/paths[component],env=env,check=True)
    # Run the binary directly so SIGINT/SIGTERM reaches its shutdown handler.
    os.chdir(ROOT);os.execve(target,[str(target)],env)

def launch():
    state=ROOT/'.cache/processes.json'
    if state.exists():raise SystemExit('Local processes already recorded; use make wait-ready or make dev-down')
    paths={'admission':'services/admission','flash-sale':'services/flash-sale','order':'services/order','analytics':'services/analytics','gateway':'gateway','doubles':'tests/doubles'}
    processes=[]
    try:
        for component,path in paths.items():
            target=ROOT/'.cache/bin'/component;target.parent.mkdir(parents=True,exist_ok=True)
            values=load_env() if component=='doubles' else application_environment(component)
            values.pop('JWT_PRIVATE_KEY_FILE',None);values.pop('SEEDED_TOKENS_FILE',None)
            values['GOCACHE']=str(ROOT/'.cache/go-build')
            if component=='doubles':
                for key in list(values):
                    if key.endswith('_DATABASE_URL') or 'PASSWORD' in key:values.pop(key,None)
            subprocess.run(['go','build','-buildvcs=false','-o',str(target),'./cmd/server'],cwd=ROOT/path,env=values,check=True)
            logpath=ROOT/'.cache/logs'/f'{component}.log';logpath.parent.mkdir(parents=True,exist_ok=True)
            with logpath.open('ab') as log:
                p=subprocess.Popen([str(target)],cwd=ROOT,env=values,stdout=log,stderr=log,start_new_session=True)
            processes.append({'component':component,'pid':p.pid,'binary':str(target)})
        state.write_text(json.dumps(processes))
    except BaseException:
        for p in processes:
            try:os.kill(p['pid'],signal.SIGTERM)
            except ProcessLookupError:pass
        raise
    print('Started five foundation processes and contract fixtures; see .cache/logs')

def stop():
    state=ROOT/'.cache/processes.json'
    if not state.exists():return
    for p in json.loads(state.read_text()):
        command=Path('/proc')/str(p['pid'])/'cmdline'
        try:
            argv=command.read_bytes().split(b'\0')
            if argv and argv[0].decode()==p['binary']:os.kill(p['pid'],signal.SIGTERM)
        except (FileNotFoundError,ProcessLookupError):pass
    deadline=time.monotonic()+10
    while True:
        running=False
        for p in json.loads(state.read_text()):
            try:running |= (Path('/proc')/str(p['pid'])/'cmdline').read_bytes().split(b'\0')[0].decode()==p['binary']
            except FileNotFoundError:pass
        if not running:break
        if time.monotonic()>=deadline:raise SystemExit('Local shutdown deadline exceeded; process record retained')
        time.sleep(0.05)
    state.unlink()
    print('Signalled only the recorded local processes; volumes and credentials retained')


def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('command',choices=['init','config','up','down','status','check','run','launch','stop']);parser.add_argument('--component');args=parser.parse_args()
    if args.command=='init':initialize()
    elif args.command=='config':compose(['config','--quiet'])
    elif args.command=='up':compose(['up','--detach','--wait','--wait-timeout','180'])
    elif args.command=='down':compose(['down']) # Retain volumes; no implicit reset.
    elif args.command=='status':compose(['ps'])
    elif args.command=='check':
        compose(['exec','-T','postgres','pg_isready','-U','postgres','-d','postgres'])
        for prefix,user,database in [('ADMISSION','admission','admission'),('FLASH_SALE','flash_sale','flash_sale'),('ORDER','order_service','order')]:
            command=f'PGPASSWORD="${prefix}_DB_PASSWORD" psql -h 127.0.0.1 -U {user} -d flashkart_{database} -v ON_ERROR_STOP=1 -Atc \"SELECT current_user\"'
            compose(['exec','-T','postgres','sh','-ec',command])
        compose(['exec','-T','postgres','psql','-U','postgres','-d','flashkart_flash_sale','-v','ON_ERROR_STOP=1','-Atc',"SELECT 1/count(*)::integer FROM pg_extension WHERE extname='btree_gist'"])
        roles=[('admission','flashkart_admission'),('flash_sale','flashkart_flash_sale'),('order_service','flashkart_order')]
        conditions=[f"has_database_privilege('{user}','{other}','CONNECT')" for user,own in roles for _,other in roles if other!=own]
        isolation="DO $$ BEGIN IF "+' OR '.join(conditions)+" THEN RAISE EXCEPTION 'service database isolation failed'; END IF; END $$;"
        compose(['exec','-T','postgres','psql','-U','postgres','-d','postgres','-v','ON_ERROR_STOP=1','-c',isolation])
        compose(['exec','-T','kafka','sh','-ec','/opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server localhost:9092 >/dev/null'])
        print('Local PostgreSQL service logins, btree_gist and Kafka API are reachable')
    elif args.command=='launch':launch()
    elif args.command=='stop':stop()
    else:run_component(args.component)
    return 0

if __name__=='__main__':
    try:raise SystemExit(main())
    except (subprocess.CalledProcessError,OSError) as exc:print(f'Development command failed: {exc}',file=sys.stderr);raise SystemExit(1)
