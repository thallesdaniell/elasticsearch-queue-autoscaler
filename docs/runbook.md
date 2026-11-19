# Runbook e homologação

## Implantação gradual

1. Use cluster e dados descartáveis com a mesma versão ECK/Elasticsearch do alvo final. Prepare backup/restauração e orçamento máximo.
2. Valide KEDA: ScaledObject Ready, HPA com métricas válidas e /scale com selector e contagem corretos. Audit deve mostrar demanda sem alterar o ECK.
3. Compare recomendações com latência de indexação, rejeições, heap, CPU e consumo real da fila. Calibre o threshold do scaler por teste de carga; crescimento da fila isolado não identifica gargalo.
4. Teste janelas no fuso correto, sobreposição, fim da janela e janela cruzando meia-noite. Espere passos/cooldown/estabilização.
5. DryRun: confirme resposta validada, count e annotations do Elasticsearch inalterados.
6. Auto, com maxStep=1 inicialmente: acompanhe count, generation ECK, Pods, PVCs e shards. Um segundo aumento deve esperar convergência e cooldown.
7. Derrube o Pod controller após um patch e antes do próximo ciclo. O novo líder deve respeitar o marcador no Elasticsearch.
8. Force erro de acesso Elasticsearch; nenhum patch deve ocorrer. Pare KEDA: o pedido persistido pode continuar sendo aplicado, conforme ADR 0004; confirme que Audit pausa novos patches. Use dados de teste para simular disco acima do limite/shards em movimento.
9. Sincronize a Application ArgoCD e confirme que count continua dinâmico, enquanto mudanças em imagem/configuração continuam sendo aplicadas.
10. Remova a demanda e encerre as janelas: count deve permanecer. Documente o resultado e só então considere produção.

## Condições comuns

| Reason | Ação |
|---|---|
| Unauthorized | Revisar owner/node-set, namespace e RBAC resourceNames |
| InvalidTarget | Confirmar nome, count, roles explícitas e ausência de master |
| ScaleStalled | Investigar a operação e capacidade após o deadline; não reduzir count automaticamente |
| PendingScale | Comparar generation, Pods por NodeSet e pending-count; procurar PVC Pending, quotas e drift |
| SafetyBlocked | Investigar health, shards, disco, certificado ou API key |
| HPA/ScaledObject sem métricas | Revisar scaler, autenticação, query, freshness e permissões /scale |
| Stabilizing / Cooldown | Espera prevista; não reiniciar para tentar ignorar a proteção |
| PatchRejected | Conferir conflito, admission/RBAC e writes concorrentes; próximo ciclo relê o alvo |

Se uma operação estiver presa, o timeout não libera novo scaling automaticamente. Corrija a causa. Não remova pending-count para esconder uma mudança revertida pelo GitOps. Para recuperação manual: coloque Audit, investigue disponibilidade e reconciliadores concorrentes, aplique um plano revisado via ECK, espere convergência e só então atualize/remova o marcador obsoleto de forma explícita.

## Credenciais

API key: o arquivo é relido em cada chamada e o volume é montado como diretório, permitindo propagação de atualização de Secret pelo kubelet. Uma troca durante o ciclo pode causar falha fechada, retomada no próximo ciclo. CA: carregada no startup e montada com subPath nesta versão; faça rollout do deployment após rotação da CA.

## Limites de operação

Não há garantia de zero impacto ao adicionar nós. O operador não gerencia ILM, shards, zonas, storage classes nem capacidade do node pool. Não resolve backlog provocado por consumidor parado, rate limit externo ou mensagem inválida. Os limites de disco/shards são bloqueios conservadores; recuperação por scale up em cluster degradado é manual no MVP.

## Testes KEDA em cluster

Confirme um único ScaledObject/HPA por CR. Injete demanda equivalente a 8 com cron em 5, depois cron em 8 com demanda em 4: deve prevalecer o maior, dentro de maxStep/maxReplicas. Verifique limites, sobreposição e fim de janela sem remoção de nós. Compare /scale.status.replicas com Pods do NodeSet, inclusive durante PendingScale. Teste RBAC negativo contra outro autoscaler e confirme que KEDA não pode escrever no Elasticsearch. Sincronize ArgoCD e confira preservação também de requestedReplicas. Não prometa freshness automática: revise demanda ao sair de Audit.

## Observabilidade e capacidade

`config/monitoring/alerts.yaml` contém regras Prometheus opcionais; carregue-as no monitoramento e ajuste o scrape job. Não são instaladas pelo Helm. O alerta de ausência de decisões usa séries já observadas: para CRs que nunca emitiram ou foram removidas, monitore também inventário esperado e scrape targets. O alerta de convergência ignora séries antigas de uma réplica que perdeu liderança.

Acompanhe eqa_replicas por kind requested/current/desired/observed/ready. ScaleStalled aparece após espera contínua em convergenceTimeoutSeconds, padrão 2700s, sem rollback. Se a política exigir um Pod por host, configure anti-affinity e requireDistinctNodes=true; zonas/fallback spot precisam de validação própria. Não inferir node pool no máximo só porque um Pod está Pending: investigue quota, afinidade, storage e scheduler.

O deadline de convergência acompanha bloqueios ECK/Pods e marcadores, não substitui alertas de SafetyBlocked por shards/disco/health. Um controlador novo não pode encurtar cooldown-until do último writer.
