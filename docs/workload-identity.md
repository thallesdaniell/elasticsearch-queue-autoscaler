# Autenticação das fontes após a migração KEDA

Este operador não usa Google ADC nem consulta Cloud Monitoring diretamente. Não configure Workload Identity GCP no ServiceAccount eqa para fornecer demanda.

Se escolher um scaler Google Cloud no KEDA, configure a identidade e as permissões na instalação KEDA, seguindo o método de autenticação da versão/scaler utilizado. Use TriggerAuthentication quando aplicável; conceda somente as permissões necessárias. Não copie bindings da implementação antiga para o novo ServiceAccount sem revisar namespace, identidade e projeto.

A API key Elasticsearch de leitura e a CA continuam montadas no operador. Veja [KEDA](keda.md) e a documentação oficial do scaler escolhido. Pub/Sub é uma fonte opcional atrás do KEDA, não um provider deste runtime.
