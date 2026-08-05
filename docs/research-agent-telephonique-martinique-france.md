# Recherche — agent téléphonique et WhatsApp en France/Martinique

Date: 2026-08-03

## Conclusion courte

Le scénario est faisable sans changer le numéro fixe connu des clients pour la
partie téléphonique : l'opérateur peut conserver le numéro et renvoyer les
appels sans réponse vers une couche SIP/Voice qui connecte l'agent vocal.

Le scénario le plus robuste pour WhatsApp est toutefois un deuxième numéro
dédié par entreprise/site. Réutiliser le fixe existant comme expéditeur
WhatsApp est possible seulement si le numéro peut recevoir le code de
vérification et n'est pas déjà utilisé par WhatsApp, ou si un fournisseur
prend explicitement en charge la coexistence Business App + API.

## Ce qui est faisable

### 1. Conserver le numéro fixe pour les appels

Un renvoi d'appel sur non-réponse permet de ne rien changer au numéro composé
par le client. La couche Voice peut ensuite alimenter Vapi, Retell, Twilio
ConversationRelay ou un agent développé sur mesure.

La portabilité existe également en France et en Martinique, mais elle implique
un changement d'opérateur et davantage de coordination. L'Arcep confirme que
la portabilité se fait à l'intérieur de territoires français définis, dont la
France métropolitaine et la Martinique :

- https://www.arcep.fr/la-regulation/grands-dossiers-thematiques-transverses/la-numerotation/portabilite-numeros-telephone-fixes-et-mobiles.html

### 2. Utiliser un numéro dédié pour WhatsApp

Un numéro WhatsApp Business Platform doit être vérifié par SMS ou appel vocal,
être au format international et ne pas être déjà enregistré dans WhatsApp. La
documentation Twilio indique qu'un numéro déjà utilisé dans WhatsApp ou
WhatsApp Business App doit normalement être supprimé de l'application avant
d'être enregistré comme sender API, et qu'il ne pourra alors plus continuer à
être utilisé dans l'application avec ce numéro :

- https://www.twilio.com/docs/whatsapp/register-senders-using-api
- https://www.twilio.com/docs/whatsapp/migrate-numbers-and-senders

Pour des confirmations transactionnelles, il faut aussi gérer l'opt-in et les
templates WhatsApp approuvés lorsque l'entreprise initie la conversation :

- https://www.twilio.com/docs/whatsapp/api

### 3. Utiliser le même numéro fixe pour Voice et WhatsApp

Ce n'est pas impossible, mais ce n'est pas le bon défaut produit :

- le numéro doit recevoir l'OTP de vérification ;
- un serveur vocal interactif ou un système téléphonique informatisé peut
  empêcher la réception correcte de l'OTP ;
- si le numéro est déjà dans WhatsApp Business App, la migration standard peut
  supprimer l'usage de l'app ;
- la coexistence app/API dépend du parcours et du partenaire choisis, donc elle
  doit être validée entreprise par entreprise.

Twilio documente par ailleurs la possibilité d'utiliser un sender WhatsApp
activé avec ses fonctions Voice, mais cela concerne des appels WhatsApp et ne
résout pas automatiquement la conservation d'un numéro fixe déjà exploité par
un opérateur :

- https://www.twilio.com/docs/voice/whatsapp-business-calling

## Fournisseurs à tester

### Twilio + agent vocal séparé

Twilio est la base la plus flexible pour le numéro, le renvoi/SIP, Voice et
WhatsApp. Son tarif publié pour la France indique environ 1,35 $/mois pour un
numéro local et 0,0100 $/minute pour recevoir un appel local. Pour la
Martinique, le tarif publié indique 0,0286 $/minute vers un numéro local et
0,3531 $/minute vers un mobile ; la disponibilité exacte des numéros doit être
vérifiée dans le catalogue avant de vendre l'offre :

- https://www.twilio.com/en-us/voice/pricing/fr
- https://www.twilio.com/fr-fr/voice/pricing/mq

Twilio ConversationRelay fournit le transport temps réel STT/TTS et laisse le
choix du LLM, mais il faut davantage d'intégration technique :

- https://www.twilio.com/en-us/products/conversational-ai/conversationrelay

### Retell

Retell propose un agent téléphonique plus directement packagé, avec un prix
publié d'environ 0,07–0,31 $/minute selon la configuration, plus les éléments
de téléphonie. Il est intéressant pour un pilote rapide, mais l'intégration du
numéro fixe existant doit être validée par SIP/portage :

- https://www.retellai.com/pricing
- https://www.retellai.com/ai-customer-service

### Vapi

Vapi est flexible et peut utiliser les identifiants Twilio pour configurer des
numéros, mais il laisse davantage de décisions techniques à prendre. C'est
intéressant si le produit doit changer de modèle vocal, STT, TTS ou LLM :

- https://docs.vapi.ai/phone-calling
- https://vapi.ai/pricing

## Recommandation produit

Pour un onboarding léger :

1. Le client conserve son numéro fixe.
2. Il active un renvoi « si pas de réponse » vers le numéro Voice de la
   plateforme.
3. La plateforme connecte l'appel à l'agent vocal.
4. La plateforme fournit un numéro WhatsApp dédié par entreprise/site.
5. Les confirmations WhatsApp sont envoyées avec opt-in et templates.
6. Dans l'interface, les deux canaux sont regroupés sous la même entreprise,
   même si les numéros techniques diffèrent.

Le même numéro pour les deux canaux doit être proposé comme option avancée,
après un test d'éligibilité du numéro et une validation du parcours WhatsApp.

## Contraintes à ne pas cacher au client

Si les appels sont enregistrés ou transcrits, l'entreprise doit informer les
interlocuteurs de l'existence, de la finalité, des destinataires, de la durée
de conservation et de leurs droits. La CNIL recommande une information orale
au début de l'appel complétée par une information détaillée accessible :

- https://www.cnil.fr/fr/cnil-direct/question/enregistrement-ou-ecoute-des-conversations-telephoniques-faut-il-informer-ses
- https://www.cnil.fr/fr/lenregistrement-des-conversations-telephoniques-afin-detablir-la-preuve-de-la-formation-dun-contrat

## Expérience pilote recommandée

Tester trois cas avec une entreprise en Martinique et une en métropole :

1. Appel sur le fixe pendant que l'entreprise ne répond pas.
2. Appel transféré vers l'agent avec annonce claire qu'il s'agit d'un agent
   automatisé.
3. Message WhatsApp de confirmation après accord explicite du client.

Mesurer le coût réel par minute, le délai de réponse, la qualité de la voix,
le taux de transfert humain et la délivrabilité WhatsApp avant de promettre un
numéro unique.

## Repères de concurrence et de prix

Les prix publics trouvés sont principalement des prix de plateforme, pas des
offres gérées incluant l'onboarding, le support, WhatsApp et la configuration
par entreprise.

| Service | Repère public | Ce que cela implique |
| --- | ---: | --- |
| Retell | environ 0,07–0,31 $/min ; numéro Retell 2 $/mois | Très bon repère pour un agent vocal prêt à déployer ; LLM, voix, téléphonie et options peuvent être séparés. |
| Bland | 0,14 $/min Start, 0,12 $/min + 299 $/mois Build, 0,11 $/min + 499 $/mois Scale | Plus simple à lire, mais le carrier téléphonique et certains transferts restent séparés. |
| ElevenLabs Reception | 99 $/mois pour 500 minutes, puis 0,40 $/minute supplémentaire | Repère produit “réceptionniste” ; les plans publiés incluent des plafonds de messages et ne constituent pas une offre WhatsApp illimitée. |
| Twilio Voice | France : numéro local 1,35 $/mois et réception locale 0,0100 $/min ; Martinique : tarifs Voice publiés séparément | Twilio est surtout une brique télécom/WhatsApp, pas une offre d'agent managée complète. |

Sources officielles :

- https://www.retellai.com/pricing
- https://www.bland.ai/pricing
- https://elevenlabs.io/docs/reception-ai/billing/plans-and-pricing
- https://www.twilio.com/en-us/voice/pricing/fr
- https://www.twilio.com/fr-fr/voice/pricing/mq

## Lecture des prix envisagés

Les deux offres envisagées correspondent à :

- 300 € / 700 minutes = environ 0,43 €/minute vendue ;
- 600 € / 1 800 minutes = environ 0,33 €/minute vendue.

Ces prix peuvent être compétitifs si l'offre inclut la configuration de
l'entreprise, une voix choisie, la base de connaissances, la supervision, les
transferts humains, le numéro, WhatsApp et le support. Ils ne sont pas
compétitifs si le client ne reçoit qu'un accès brut à un agent vocal, car les
plateformes affichent des coûts de plateforme inférieurs.

## Pourquoi éviter “WhatsApp illimité” sans définition

Twilio facture actuellement un montant par message, auquel peuvent s'ajouter
les frais Meta liés aux templates. Dans une fenêtre de service de 24 heures
ouverte par un message du client, les messages libres et certains messages
utilitaires peuvent être exonérés de frais Meta, mais le fournisseur peut
continuer à appliquer ses propres frais. Hors fenêtre, les messages initiés par
l'entreprise nécessitent des templates approuvés et peuvent être facturés.

- https://www.twilio.com/en-us/whatsapp/pricing?locale=en
- https://www.twilio.com/docs/whatsapp/tutorial/send-whatsapp-notification-messages-templates

Une formulation plus sûre serait : « WhatsApp inclus avec usage raisonnable,
messages transactionnels inclus jusqu'à X par mois, puis facturation au coût
réel ou validation préalable ». Cela protège la marge contre un client qui
utiliserait l'offre comme outil de campagne marketing.
