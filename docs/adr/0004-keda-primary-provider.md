# ADR 0004: KEDA como provider principal via /scale

Status: aceito, 2026-10-11. Substitui o provider Pub/Sub direto e a política de falha de Monitoring do desenho inicial.

Contexto: o usuário decidiu centralizar a demanda no KEDA, sem acoplar o operador a Google Cloud. Precisamos preservar segurança ECK e combinar métricas com horários.

Decisão: expor /scale na CRD com spec.requestedReplicas, status.replicas e status.selector. KEDA/HPA administra apenas a demanda. O operador mantém limites, passos, estabilização, cooldown e validações, sendo o único writer autorizado do count ECK. Não usar webhook para comandos imperativos. Remover cliente Monitoring, ADC e campos específicos de Pub/Sub da API/runtime.

Agendamento: triggers cron KEDA são o padrão dos exemplos. Pisos locais existentes permanecem opcionais e combinados por máximo. Nunca reduzir automaticamente, mesmo se o HPA solicitar zero.

Status: separar count aplicado no ECK de Pods observados e prontos. Selector inclui cluster e StatefulSet do NodeSet, sem escrever nesse StatefulSet. Alterações de requestedReplicas não reiniciam estabilização; queda da recomendação reinicia o piso sustentado e mudança de política reinicia a janela.

Freshness: /scale representa intenção persistida, sem timestamp/TTL. Não inferir saúde do provider de uma ausência de writes, pois o HPA pode manter demanda válida sem reescrever. Não foi implementada expiração ou watchdog. Parar KEDA não cancela intenção. Audit pausa a aplicação; alertas e qualidade da origem pertencem ao KEDA/monitoramento. Um contrato de freshness forte exigiria extensão separada.

Consequências: operador perde dependência operacional de GCP, ganha dependência de integração KEDA/HPA e exige ownership GitOps também em requestedReplicas. Esta é uma mudança incompatível da API alpha: remover pubSub/backlogPerNode/maxMetricAgeSeconds/oldestAgeSeconds dos manifests. Homologar KEDA/HPA/ECK reais antes de produção; envtest valida somente o contrato Kubernetes.
