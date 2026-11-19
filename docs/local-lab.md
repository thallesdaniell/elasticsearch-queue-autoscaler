# Laboratório local com KEDA e Docker Compose

O Compose sobe K3s, um Kubernetes dentro de um container privilegiado. O script instala **KEDA, HPA do Kubernetes, ECK, Elasticsearch, Prometheus e este operador reais** nesse cluster. Somente o backlog é sintético. Não há fork do KEDA nem external scaler customizado: usamos os scalers nativos Prometheus e cron e o `/scale` da nossa CRD.

## Pré-requisitos

- Docker Engine ou Docker Desktop com containers Linux e Docker Compose v2.
- Python 3.10+ no host, sem bibliotecas adicionais; `make` opcional.
- Reservar aproximadamente 4 CPUs, 10 GiB RAM e 15 GiB livres para imagens e dados. O consumo varia com plataforma e downloads.
- Internet para imagens e charts; porta local 16443 livre.

O laboratório usa container privilegiado, master único e `emptyDir`. É descartável, sem dados de produção e sem credenciais cloud. Não é configuração de produção. Não precisa alterar `vm.max_map_count`, pois o Elasticsearch de laboratório desabilita mmap.

Versões fixadas para este cenário: K3s v1.32.4-k3s1, KEDA/chart 2.17.2, ECK/chart 3.0.0, Elasticsearch 8.17.3, Helm 3.17.3 e Prometheus 3.2.1. São versões de laboratório, não uma recomendação de versões atuais para produção. O funcionamento conjunto deve ser confirmado pela execução abaixo.

## Execução

Na raiz do repositório:

```bash
make local-up
make local-test
make local-status
```

Sem `make`:

```bash
python3 hack/local/lab.py up
python3 hack/local/lab.py test
python3 hack/local/lab.py status
```

`up` constrói a imagem do código local e importa no containerd do K3s; não precisa publicar imagem. Começa em **Audit**. O script só usa `docker compose exec k3s kubectl`, nunca seu contexto Kubernetes ou kubeconfig do host. Não execute dois laboratórios simultâneos com o mesmo nome Compose `eqa-local`.

O bootstrap usa a senha `elastic` gerada pelo ECK apenas em um Job temporário para criar um índice `lab-check` com três shards primários e zero réplicas e uma API key com `cluster: monitor` e `indices: monitor` sobre todos os índices, incluindo restritos. Não concede leitura de documentos, escrita nem alteração de settings à chave do operador. Captura a chave sem imprimir e a grava em `eqa-es-monitor`; depois remove o Job. Essa chave é somente para as verificações do operador. A senha de administrador continua no Secret do ECK. Administradores do cluster local podem acessar esses Secrets. Recriar com `down`/`up` evita acumular chaves em novas tentativas de bootstrap.

## Cenário automatizado

Execute `test` logo após `up`: as duas janelas cron ficam ativas por aproximadamente duas horas a partir do bootstrap, em UTC.

| Etapa | Evidência exigida pelo script |
|---|---|
| Duas janelas cron com pisos 1 e 2 | KEDA/HPA escreve 2 em `spec.requestedReplicas`, usando o maior piso |
| Backlog 250, alvo 100 por nó | KEDA/HPA escreve 3 via `/scale`, superando o piso 2 |
| Audit | Condição Audit da geração atual e contagem ECK permanece 1 |
| DryRun | Condição DryRun da geração atual confirma patch aceito sem persistência; ECK permanece 1 |
| Auto | ECK converge para 3 Data Nodes e cluster green |
| Pedido de redução | Pausa KEDA, espera reconhecimento, zera backlog e pede 1; ECK continua em 3 por 75 segundos |
| Escopo | Master não autorizado continua com contagem 1 |

O último teste pausa KEDA para que o HPA não sobrescreva o pedido menor, verificando a proteção do operador independentemente da política `scaleDown: Disabled` do HPA. O cenário termina em Audit e deixa KEDA pausado, inclusive quando uma etapa falha após iniciar o cenário. Se o cluster estiver inacessível, a tentativa de retornar a Audit também poderá falhar.

`test` exige cluster novo com um Data Node e modo Audit. Para repetir, destrua e recrie. `up` não é uma rotina de atualização de clusters existentes: reaplica a topologia inicial do laboratório.

```bash
make local-down
make local-up
make local-test
```

`local-down` remove containers e volumes **deste laboratório**, incluindo todos os dados Elasticsearch locais.

## Inspeção manual

```bash
python3 hack/local/lab.py backlog 250
make local-status
docker compose exec k3s kubectl -n elastic-system get elasticsearchqueueautoscaler search-queue -o yaml
docker compose exec k3s kubectl -n elastic-system describe hpa keda-hpa-search-demand
docker compose exec k3s kubectl -n elastic-system logs deployment/eqa --tail=100
docker compose exec k3s kubectl -n keda logs deployment/keda-operator --tail=100
```

Mudar backlog não ativa Auto nem retoma um KEDA pausado. Após `test`, prefira recriar o laboratório para nova execução completa. Pausar KEDA sozinho não cancela intenção já persistida; Audit bloqueia aplicação pelo nosso operador.

Se falhar, examine conditions da CR, HPA, eventos e Pods, especialmente `Pending`, `OOMKilled`, falhas de download e capacidade de memória. Timeouts são falhas do teste, não resultados aprovados.

## Credenciais e KEDA customizado

| Acesso | Responsável |
|---|---|
| Pub/Sub, Datadog ou fonte de demanda autenticada | KEDA, usando `TriggerAuthentication`/`ClusterTriggerAuthentication` e identidade/Secret apropriado ao scaler |
| Prometheus e cron deste laboratório | Sem credencial externa; endpoints internos, dados sintéticos |
| API Kubernetes e `/scale` | ServiceAccounts/RBAC do KEDA e HPA |
| Leitura e patch do Elasticsearch CR, Lease, status e eventos | ServiceAccount/RBAC do nosso operador |
| Saúde, shards e disco Elasticsearch | API key somente monitor + CA montadas no nosso operador |

Secrets referenciados por `TriggerAuthentication` normalmente ficam no namespace da aplicação, não necessariamente no namespace `keda`. Workload Identity usa identidade federada; não exige armazenar uma chave GCP JSON. A identidade exata depende do scaler e da configuração KEDA.

Se uma fonte não tiver scaler nativo, KEDA aceita external scalers via gRPC. Isso seria outro serviço que fornece métricas/atividade ao KEDA, sem substituir nosso validador de segurança. Não é necessário para Prometheus, cron ou Pub/Sub.

## Limites da validação

Este teste verifica o caminho real de controle, não desempenho de ingestão, distribuição útil de shards, falhas de zona, ArgoCD, credenciais GCP/Datadog ou segurança de scale down. O índice pequeno verifica a consulta de shards, mas não há carga de ingestão real: adicionar Data Nodes não prova aumento de throughput.

Em 2026-10-12 foram executados `make verify` e cinco testes locais Python: aprovados. O YAML Compose foi lido com parser YAML. **O cenário Docker/KEDA/ECK não foi executado no ambiente de desenvolvimento da assistência**, pois não havia Docker/daemon disponível. Não confundir testes `envtest` com KEDA/ECK rodando. `make local-test` é o gate de execução real na máquina com Docker.

Referências: [KEDA custom resources](https://keda.sh/docs/2.17/concepts/scaling-deployments/), [autenticação](https://keda.sh/docs/2.17/concepts/authentication/), [external scalers](https://keda.sh/docs/2.17/concepts/external-scalers/), [K3s Compose](https://github.com/k3s-io/k3s/blob/main/docker-compose.yml).

`/_cat/shards` exige monitoramento de cluster e índices: [permissões documentadas pelo Elasticsearch](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/cat-shards.html). O exemplo cobre `*` e índices restritos para não apresentar inventário parcial como verificação global. A chave continua sem permissões de escrita.
