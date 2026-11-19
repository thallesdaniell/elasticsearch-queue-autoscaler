# Corte de escritores existentes

O lock deste operador coordena suas próprias instâncias. CronJobs, scripts, outro controlador e ArgoCD não passam a respeitá-lo automaticamente.

1. Inventariar todos os escritores do count do Elasticsearch, inclusive Jobs já iniciados, scripts manuais, schedule deployments e GitOps. Identificar também escritores de index.number_of_replicas.
2. Instalar este operador em Audit e comparar recomendações com o atual, mantendo limites conservadores e dados de teste. Não executar dois writers em Auto.
3. Configurar ArgoCD para delegar apenas count do NodeSet autorizado e preservar annotations de recuperação. Testar que outras mudanças ainda sincronizam.
4. Validar DryRun, permissões, nomes exatos, métrica/projeto/subscription e fuso. Registrar diferenças antes de mudar a autoridade.
5. Numa janela de corte, suspender os escritores antigos. Suspender CronJob evita Jobs futuros, mas **não interrompe Jobs já ativos**; aguardar ou encerrar de forma revisada as execuções existentes. Confirmar que deployments antigos deixaram de reconciliar.
6. Esperar a operação anterior do ECK terminar. Autorizar o novo CR nas annotations. Ativar Auto em um cluster por vez, maxStep=1 no primeiro teste.
7. Acompanhar generation, Pods/PVCs, shards, latência e custo antes de ampliar.

Rollback de controlador: colocar o novo em Audit, confirmar que não há patch em trânsito e aguardar convergência do ECK; só então devolver autoridade ao writer anterior. Isso não reduz automaticamente os nós já adicionados nem desfaz mudanças assíncronas. Revisar qualquer redução como uma operação independente.

Merge de código não garante ausência de implantação: confirme se automações ou auto-sync observam o caminho dos manifests.

## Migração v0.1 (Pub/Sub direto) para v0.2 (KEDA)

1. Coloque o autoscaler em Audit e espere qualquer operação ECK já enviada concluir.
2. Revise/aplique a CRD atualizada separadamente do Helm. Faça backup do manifesto alpha anterior; os campos removidos deixam de fazer parte do schema.
3. Remova sources.pubSub, backlogPerNode, oldestAgeSeconds e maxMetricAgeSeconds. Configure provider: KEDA e requestedReplicas inicialmente igual ao count existente; ajuste min/max.
4. Remova pubsubEnabled e a anotação GCP da ServiceAccount do operador, se só era usada para Monitoring. Migre autenticação e critérios de demanda para o scaler KEDA escolhido. A antiga regra oldestAge -> current+1 não é reproduzida implicitamente.
5. Atualize imagem/controller/chart e confirme status.replicas e selector antes de instalar ScaledObject. Configure permissões /scale e ignoreDifferences para requestedReplicas.
6. Escolha cron KEDA ou piso local para cada janela, evitando duplicação. Só um ScaledObject escreve em cada CR. Mantenha HPA scaleDown Disabled e restoreToOriginalReplicaCount false.
7. Compare recomendações em Audit/DryRun com fontes reais e teste falha de scaler, sobreposição e convergência. Reconheça que pedido persistido não expira quando KEDA falha.
8. Antes de Auto, revise requestedReplicas atual e confirme ausência de writers legados. Voltar para Audit pausa novos patches sem reverter count já aplicado.

## Adoção das proteções revisadas

Antes de autorizar dois NodeSets, coloque todas as CRs em Audit e confirme que o Elasticsearch convergiu. Configure owners como mapa nome-do-NodeSet -> nome-da-CR. Para marcadores antigos sem pending-node-set, mantenha a annotation legada node-set até migrar o marcador ou registre explicitamente o NodeSet ao qual o pending-count pertence; nunca atribua o count pendente a outro alvo. Mapa malformado não faz fallback. Os novos patches gravam pending-node-set atomicamente.

Habilitar requireDistinctNodes exige hosts distintos no alvo e política de agendamento compatível; isso não cria anti-affinity. Revise convergenceTimeoutSeconds e encaminhe ScaleStalled para intervenção, sem apagar marcadores para liberar novos aumentos. O timeout não é gatilho para reativar jobs antigos.

A retirada dos legados só está autorizada operacionalmente após os gates de SPEC.md, especialmente custo sem downscale e capacidade útil percolate. Este trabalho não executou o corte.
