#!/usr/bin/env python3
"""Isolated Compose cluster harness. Never uses the host kubeconfig."""
import argparse
from datetime import datetime, timedelta, timezone
import base64
import json
from pathlib import Path
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
NS = 'elastic-system'
RESOURCE = 'elasticsearchqueueautoscalers.autoscaling.queue-autoscaler.io'
COMPOSE = ['docker', 'compose', '-f', str(ROOT / 'compose.yaml')]


def run(args, data=None, capture=True):
    result = subprocess.run(args, input=data, text=True, capture_output=capture, cwd=ROOT)
    if result.returncode:
        # Do not dump command arguments/stdin: they can contain bootstrap secrets.
        raise RuntimeError(result.stderr if capture else 'Command failed')
    return result.stdout if capture else ''


def compose(*args, **kwargs):
    return run(COMPOSE + list(args), **kwargs)


def kube(*args, data=None):
    return compose('exec', '-T', 'k3s', 'kubectl', *args, data=data)


def helm(*args):
    # Compose run replaces command, so supply the wrapper explicitly.
    return compose('run', '--rm', 'helm',
                   'sed "s/127.0.0.1/k3s/g" /input/kubeconfig.yaml > /tmp/kubeconfig; '
                   'export KUBECONFIG=/tmp/kubeconfig; exec helm "$@"', '--', *args)


def apply(obj):
    return kube('apply', '-f', '-', data=json.dumps(obj))


def get(kind, name):
    return json.loads(kube('-n', NS, 'get', kind, name, '-o', 'json'))


def patch(kind, name, obj):
    return kube('-n', NS, 'patch', kind, name, '--type=merge', '-p', json.dumps(obj))


def wait(label, predicate, seconds=900):
    print(f'Waiting: {label}', flush=True)
    deadline = time.monotonic() + seconds
    last = None
    while time.monotonic() < deadline:
        try:
            if predicate():
                print(f'PASS: {label}', flush=True)
                return
        except (RuntimeError, KeyError, ValueError) as exc:
            last = str(exc)
        time.sleep(5)
    raise RuntimeError(f'Timeout: {label}; last error: {last}')


def count(es=None):
    es = es or get('elasticsearch', 'search')
    return next(n['count'] for n in es['spec']['nodeSets'] if n['name'] == 'hot-data')


def ready():
    es = get('elasticsearch', 'search')
    return (es.get('status', {}).get('phase') == 'Ready'
            and es['status'].get('health') == 'green'
            and es['status'].get('observedGeneration') == es['metadata']['generation'])


def backlog(value):
    if value < 0:
        raise ValueError('Backlog must be nonnegative')
    kube('-n', NS, 'exec', 'deploy/backlog', '--', 'python', '-c',
         f'from pathlib import Path; Path("/tmp/backlog").write_text("{value}")')


def deployment(name, image, **container):
    return {'apiVersion': 'apps/v1', 'kind': 'Deployment', 'metadata': {'name': name, 'namespace': NS},
            'spec': {'replicas': 1, 'selector': {'matchLabels': {'app': name}},
                     'template': {'metadata': {'labels': {'app': name}}, 'spec': {
                         'containers': [{'name': name, 'image': image, **container}]}}}}


def service(name, port):
    return {'apiVersion': 'v1', 'kind': 'Service', 'metadata': {'name': name, 'namespace': NS},
            'spec': {'selector': {'app': name}, 'ports': [{'port': port, 'targetPort': port}]}}


def bootstrap_key():
    # Temporary Job consumes mounted secrets. The API key is captured, never printed.
    code = '''import base64,json,ssl,urllib.request,urllib.error
from pathlib import Path
password=Path('/auth/elastic').read_text().strip()
headers={'Content-Type':'application/json','Authorization':'Basic '+base64.b64encode(('elastic:'+password).encode()).decode()}
context=ssl.create_default_context(cafile='/ca/tls.crt')
index=urllib.request.Request('https://search-es-http:9200/lab-check',method='PUT',data=json.dumps({'settings':{'number_of_shards':3,'number_of_replicas':0}}).encode(),headers=headers)
try:
 urllib.request.urlopen(index,context=context).close()
except urllib.error.HTTPError as e:
 if e.code != 400 or 'resource_already_exists_exception' not in e.read().decode(): raise
body=json.dumps({'name':'eqa-local-monitor','role_descriptors':{'monitor':{'cluster':['monitor'],'indices':[{'names':['*'],'privileges':['monitor'],'allow_restricted_indices':True}]}}}).encode()
req=urllib.request.Request('https://search-es-http:9200/_security/api_key',data=body,headers={'Content-Type':'application/json','Authorization':'Basic '+base64.b64encode(('elastic:'+password).encode()).decode()})
with urllib.request.urlopen(req,context=ssl.create_default_context(cafile='/ca/tls.crt')) as r:
 print(json.load(r)['encoded'])
'''
    kube('-n', NS, 'delete', 'job', 'bootstrap-key', '--ignore-not-found=true')
    job = {'apiVersion': 'batch/v1', 'kind': 'Job', 'metadata': {'name': 'bootstrap-key', 'namespace': NS},
           'spec': {'backoffLimit': 2, 'template': {'spec': {'restartPolicy': 'Never',
                'containers': [{'name': 'key', 'image': 'python:3.12-alpine', 'command': ['python', '-c', code],
                                'volumeMounts': [{'name': 'auth', 'mountPath': '/auth', 'readOnly': True},
                                                 {'name': 'ca', 'mountPath': '/ca', 'readOnly': True}]}],
                'volumes': [{'name': 'auth', 'secret': {'secretName': 'search-es-elastic-user'}},
                            {'name': 'ca', 'secret': {'secretName': 'search-es-http-certs-public'}}]}}}}
    apply(job)
    try:
        kube('-n', NS, 'wait', '--for=condition=complete', 'job/bootstrap-key', '--timeout=180s')
        key = kube('-n', NS, 'logs', 'job/bootstrap-key').strip()
        if not key or '\n' in key:
            raise RuntimeError('Invalid bootstrap response')
        apply({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'eqa-es-monitor', 'namespace': NS},
               'type': 'Opaque', 'data': {'api-key': base64.b64encode(key.encode()).decode()}})
    finally:
        kube('-n', NS, 'delete', 'job', 'bootstrap-key', '--ignore-not-found=true')


def up():
    compose('up', '-d', 'k3s', capture=False)
    wait('Kubernetes API', lambda: 'ok' in kube('get', '--raw=/readyz'), 240)
    kube('wait', '--for=condition=Ready', 'node', '--all', '--timeout=240s')
    print('Installing pinned KEDA and ECK lab versions', flush=True)
    helm('upgrade', '--install', 'keda', 'keda', '--repo', 'https://kedacore.github.io/charts',
         '--version', '2.17.2', '-n', 'keda', '--create-namespace', '--wait', '--timeout', '5m')
    helm('upgrade', '--install', 'elastic-operator', 'eck-operator', '--repo', 'https://helm.elastic.co',
         '--version', '3.0.0', '-n', 'elastic-system', '--create-namespace', '--wait', '--timeout', '5m')
    kube('apply', '-f', '/repo/config/crd/bases')
    kube('wait', '--for=condition=Established', 'crd/elasticsearchqueueautoscalers.autoscaling.queue-autoscaler.io', '--timeout=60s')
    def nodeset(name, roles, memory):
        return {'name': name, 'count': 1, 'config': {'node.roles': roles, 'node.store.allow_mmap': False},
                'podTemplate': {'spec': {'containers': [{'name': 'elasticsearch',
                     'resources': {'requests': {'cpu': '250m', 'memory': memory}, 'limits': {'memory': memory}}}],
                     'volumes': [{'name': 'elasticsearch-data', 'emptyDir': {}}]}}}
    # Disposable storage and a single master are intentional lab-only settings.
    apply({'apiVersion': 'elasticsearch.k8s.elastic.co/v1', 'kind': 'Elasticsearch',
           'metadata': {'name': 'search', 'namespace': NS,
                        'annotations': {'queue-autoscaler.io/owners': '{"hot-data":"search-queue"}'}},
           'spec': {'version': '8.17.3', 'nodeSets': [nodeset('master', ['master'], '1Gi'),
                                                   nodeset('hot-data', ['data', 'ingest'], '1Gi')]}})
    wait('Elasticsearch green', ready)
    bootstrap_key()
    wait('seed index green', ready)
    print('Building and importing local operator image', flush=True)
    run(['docker', 'build', '-t', 'elasticsearch-queue-autoscaler:dev', '.'], capture=False)
    # Stream image without keeping a large archive or depending on a registry.
    producer = subprocess.Popen(['docker', 'save', 'elasticsearch-queue-autoscaler:dev'], stdout=subprocess.PIPE)
    try:
        consumer = subprocess.run(COMPOSE + ['exec', '-T', 'k3s', 'ctr', 'images', 'import', '-'], stdin=producer.stdout)
        producer.stdout.close()
        if producer.wait() or consumer.returncode:
            raise RuntimeError('Image import failed')
    finally:
        if producer.poll() is None:
            producer.terminate()
    helm('upgrade', '--install', 'eqa', '/repo/charts/elasticsearch-queue-autoscaler', '-n', NS,
         '--set', 'replicaCount=1', '--set', 'image.repository=elasticsearch-queue-autoscaler',
         '--set', 'image.tag=dev', '--set', 'image.pullPolicy=Never', '--wait', '--timeout', '3m')
    exporter = (ROOT / 'hack/local/exporter.py').read_text()
    apply({'apiVersion': 'v1', 'kind': 'ConfigMap', 'metadata': {'name': 'backlog-code', 'namespace': NS}, 'data': {'exporter.py': exporter}})
    deploy = deployment('backlog', 'python:3.12-alpine', command=['python', '/app/exporter.py'],
                        volumeMounts=[{'name': 'code', 'mountPath': '/app', 'readOnly': True}])
    deploy['spec']['template']['spec']['volumes'] = [{'name': 'code', 'configMap': {'name': 'backlog-code'}}]
    apply(deploy)
    apply(service('backlog', 8080))
    prometheus = 'global:\n  scrape_interval: 5s\nscrape_configs:\n  - job_name: backlog\n    static_configs:\n      - targets: ["backlog:8080"]\n'
    apply({'apiVersion': 'v1', 'kind': 'ConfigMap', 'metadata': {'name': 'prometheus', 'namespace': NS}, 'data': {'prometheus.yml': prometheus}})
    deploy = deployment('prometheus', 'prom/prometheus:v3.2.1', args=['--config.file=/etc/prometheus/prometheus.yml', '--storage.tsdb.path=/prometheus'],
                        volumeMounts=[{'name': 'config', 'mountPath': '/etc/prometheus', 'readOnly': True}, {'name': 'data', 'mountPath': '/prometheus'}])
    deploy['spec']['template']['spec']['volumes'] = [{'name': 'config', 'configMap': {'name': 'prometheus'}}, {'name': 'data', 'emptyDir': {}}]
    apply(deploy)
    apply(service('prometheus', 9090))
    kube('-n', NS, 'rollout', 'status', 'deployment/backlog', '--timeout=180s')
    kube('-n', NS, 'rollout', 'status', 'deployment/prometheus', '--timeout=180s')
    apply({'apiVersion': 'autoscaling.queue-autoscaler.io/v1alpha1', 'kind': 'ElasticsearchQueueAutoscaler',
           'metadata': {'name': 'search-queue', 'namespace': NS}, 'spec': {
               'provider': 'KEDA', 'mode': 'Audit', 'requestedReplicas': 1,
               'targetRef': {'name': 'search', 'nodeSet': 'hot-data'},
               'scaling': {'minReplicas': 1, 'maxReplicas': 3, 'maxStep': 1, 'pollSeconds': 15,
                           'stabilizationSeconds': 0, 'cooldownSeconds': 60, 'convergenceTimeoutSeconds': 600},
               'safety': {'requiredHealth': 'green', 'maxDiskUsedPercent': 95, 'maxShardsPerNode': 800,
                          'blockScaleDown': True, 'requireDistinctNodes': False}}})
    kube('apply', '-f', '/repo/config/samples/keda-rbac.yaml')
    wait('operator publishes /scale selector', lambda: bool(get(RESOURCE, 'search-queue').get('status', {}).get('selector')), 120)
    now = datetime.now(timezone.utc)
    start, end = now - timedelta(minutes=5), now + timedelta(hours=2)
    cron = {'timezone': 'Etc/UTC', 'start': start.strftime('%M %H * * *'), 'end': end.strftime('%M %H * * *')}
    apply({'apiVersion': 'keda.sh/v1alpha1', 'kind': 'ScaledObject', 'metadata': {'name': 'search-demand', 'namespace': NS},
           'spec': {'scaleTargetRef': {'apiVersion': 'autoscaling.queue-autoscaler.io/v1alpha1', 'kind': 'ElasticsearchQueueAutoscaler', 'name': 'search-queue'},
                    'pollingInterval': 5, 'minReplicaCount': 1, 'maxReplicaCount': 3,
                    'advanced': {'restoreToOriginalReplicaCount': False, 'horizontalPodAutoscalerConfig': {'behavior': {
                        'scaleDown': {'selectPolicy': 'Disabled'}, 'scaleUp': {'stabilizationWindowSeconds': 0}}}},
                    'triggers': [{'type': 'prometheus', 'metricType': 'AverageValue', 'metadata': {
                        'serverAddress': 'http://prometheus.elastic-system.svc:9090',
                        'query': 'max(lab_backlog and (time() - timestamp(lab_backlog) < 30))',
                        'threshold': '100', 'ignoreNullValues': 'false'}},
                        {'type': 'cron', 'name': 'floor-one', 'metadata': {**cron, 'desiredReplicas': '1'}},
                        {'type': 'cron', 'name': 'floor-two', 'metadata': {**cron, 'desiredReplicas': '2'}}]}})
    kube('-n', NS, 'wait', '--for=condition=Ready', 'scaledobject/search-demand', '--timeout=180s')
    print('Lab ready in Audit. Run: python3 hack/local/lab.py test')


def decision(reason):
    obj = get(RESOURCE, 'search-queue')
    return any(c.get('reason') == reason and c.get('observedGeneration') == obj['metadata']['generation']
               for c in obj.get('status', {}).get('conditions', []))


def observe_constant(expected, seconds=75):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if count() != expected:
            raise RuntimeError(f'Unexpected data count; expected {expected}')
        time.sleep(5)


def scenario():
    if count() != 1 or get(RESOURCE, 'search-queue')['spec']['mode'] != 'Audit':
        raise RuntimeError('Tests require a fresh Audit lab with one data node. Recreate with down, then up.')
    try:
        wait('overlapping KEDA cron floors select 2, not their sum', lambda: get(RESOURCE, 'search-queue')['spec']['requestedReplicas'] == 2, 180)
        backlog(250)
        wait('real KEDA/HPA requests 3 replicas through /scale', lambda: get(RESOURCE, 'search-queue')['spec']['requestedReplicas'] == 3, 180)
        wait('operator evaluates Audit intent', lambda: decision('Audit'), 120)
        observe_constant(1, 45)
        print('PASS: Audit preserves ECK count', flush=True)
        patch(RESOURCE, 'search-queue', {'spec': {'mode': 'DryRun'}})
        wait('API-server DryRun accepted', lambda: decision('DryRun'), 120)
        observe_constant(1, 45)
        print('PASS: DryRun preserves ECK count', flush=True)
        patch(RESOURCE, 'search-queue', {'spec': {'mode': 'Auto'}})
        wait('Auto reaches 3 data nodes', lambda: count() == 3 and ready(), 900)
        # Stop HPA writes to explicitly test operator no-downscale independently of HPA policy.
        kube('-n', NS, 'annotate', 'scaledobject/search-demand', 'autoscaling.keda.sh/paused=true', '--overwrite')
        wait('KEDA acknowledges pause', lambda: any(c['type'] == 'Paused' and c['status'] == 'True'
             for c in get('scaledobject', 'search-demand').get('status', {}).get('conditions', [])), 120)
        backlog(0)
        patch(RESOURCE, 'search-queue', {'spec': {'requestedReplicas': 1}})
        observe_constant(3)
        print('PASS: zero backlog and request=1 do not downscale ECK', flush=True)
        es = get('elasticsearch', 'search')
        if next(n['count'] for n in es['spec']['nodeSets'] if n['name'] == 'master') != 1:
            raise RuntimeError('Unauthorized master NodeSet changed')
        print('PASS: master NodeSet preserved. Live scenario complete.', flush=True)
    finally:
        # Audit is required to stop persistent intent; pausing KEDA alone is insufficient.
        patch(RESOURCE, 'search-queue', {'spec': {'mode': 'Audit'}})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['up', 'test', 'status', 'backlog', 'down'])
    parser.add_argument('value', type=int, nargs='?')
    args = parser.parse_args()
    if args.action == 'up':
        up()
    elif args.action == 'test':
        scenario()
    elif args.action == 'backlog':
        if args.value is None:
            parser.error('backlog requires a numeric value')
        backlog(args.value)
    elif args.action == 'status':
        print(kube('-n', NS, 'get', 'elasticsearch,elasticsearchqueueautoscaler,scaledobject,hpa,pods'))
    else:
        compose('down', '--volumes', capture=False)


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, FileNotFoundError) as error:
        sys.exit(str(error))
