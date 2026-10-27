### Configuration lys-shell - Module Éclairage nocturne

## Configuration du module Éclairage nocturne

settings-modules-nightlight-temperature = Température
    .description = Température de couleur en Kelvin lorsque le filtre est activé (1000-20000)

settings-modules-nightlight-gamma = Gamma
    .description = Pourcentage de gamma de l'écran lorsque le filtre est activé (0-200)

settings-modules-nightlight-auto-schedule = Planification auto
    .description = Activer automatiquement la nuit et désactiver le jour, selon le lever/coucher du soleil local. La position est détectée via GeoClue, avec repli sur les coordonnées ci-dessous

settings-modules-nightlight-latitude = Latitude
    .description = Latitude de repli en degrés décimaux (nord positif, -90 à 90), utilisée quand GeoClue est indisponible

settings-modules-nightlight-longitude = Longitude
    .description = Longitude de repli en degrés décimaux (est positif, -180 à 180), utilisée quand GeoClue est indisponible

settings-modules-nightlight-icon-off = Icône désactivée
    .description = Icône lorsque le filtre est désactivé

settings-modules-nightlight-icon-on = Icône activée
    .description = Icône lorsque le filtre est activé

settings-modules-nightlight-format = Format
    .description = Chaîne de format du libellé. Espaces réservés : {"{{ status }}"}, {"{{ temp }}"}, {"{{ gamma }}"}, {"{{ config_temp }}"}, {"{{ config_gamma }}"}

settings-modules-nightlight-border-show = Afficher la bordure
    .description = Afficher une bordure autour du bouton

settings-modules-nightlight-border-color = Couleur de la bordure
    .description = Jeton de couleur de la bordure

settings-modules-nightlight-icon-show = Afficher l'icône
    .description = Afficher l'icône du module

settings-modules-nightlight-icon-color = Couleur de l'icône
    .description = Couleur de premier plan de l'icône

settings-modules-nightlight-icon-bg-color = Arrière-plan de l'icône
    .description = Couleur d'arrière-plan du conteneur d'icône

settings-modules-nightlight-label-show = Afficher le libellé
    .description = Afficher le libellé

settings-modules-nightlight-label-color = Couleur du libellé
    .description = Couleur du texte du libellé

settings-modules-nightlight-label-max-length = Longueur max. du libellé
    .description = Nombre maximal de caractères avant troncature

settings-modules-nightlight-button-bg-color = Arrière-plan du bouton
    .description = Couleur d'arrière-plan du bouton

settings-modules-nightlight-left-click = Clic gauche
    .description = Action au clic gauche. Utiliser « :toggle » pour l'activation/désactivation intégrée, vide pour aucune action, ou une commande

settings-modules-nightlight-right-click = Clic droit
    .description = Commande à exécuter au clic droit

settings-modules-nightlight-middle-click = Clic central
    .description = Commande à exécuter au clic central

settings-modules-nightlight-scroll-up = Défilement vers le haut
    .description = Commande à exécuter au défilement vers le haut

settings-modules-nightlight-scroll-down = Défilement vers le bas
    .description = Commande à exécuter au défilement vers le bas
