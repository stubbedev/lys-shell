### Configuration lys-shell - Module Météo

## Configuration du module Météo

settings-modules-weather-provider = Fournisseur
    .description = Fournisseur de données météorologiques

settings-modules-weather-location = Emplacement
    .description = Nom de ville ou coordonnées « lat,lon »

settings-modules-weather-units = Unité de température
    .description = Métrique (Celsius) ou Impérial (Fahrenheit)

settings-modules-weather-format = Format du libellé
    .description = Chaîne de format avec espaces réservés : {"{{ temp }}"}, {"{{ temp_unit }}"}, {"{{ feels_like }}"}, {"{{ condition }}"}, {"{{ humidity }}"}, {"{{ wind_speed }}"}, {"{{ wind_dir }}"}, {"{{ high }}"}, {"{{ low }}"}

settings-modules-weather-refresh-interval = Intervalle de rafraîchissement
    .description = Intervalle d'interrogation en secondes

settings-modules-weather-visual-crossing-key = Clé Visual Crossing
    .description = Clé d'API pour Visual Crossing. Utiliser $NOM_VAR pour référencer des variables .env

settings-modules-weather-weatherapi-key = Clé WeatherAPI
    .description = Clé d'API pour WeatherAPI.com. Utiliser $NOM_VAR pour référencer des variables .env

settings-modules-weather-icon-name = Icône de repli
    .description = Icône affichée lorsque les données météo sont indisponibles

settings-modules-weather-border-show = Afficher la bordure
    .description = Afficher une bordure autour du bouton

settings-modules-weather-border-color = Couleur de la bordure
    .description = Jeton de couleur de la bordure

settings-modules-weather-icon-show = Afficher l'icône
    .description = Afficher l'icône du module

settings-modules-weather-tooltip = Afficher l'infobulle
    .description = Afficher l'état du module dans une infobulle au survol

settings-modules-weather-icon-color = Couleur de l'icône
    .description = Couleur de premier plan de l'icône

settings-modules-weather-icon-bg-color = Arrière-plan de l'icône
    .description = Couleur d'arrière-plan du conteneur d'icône

settings-modules-weather-label-show = Afficher le libellé
    .description = Afficher le libellé de température

settings-modules-weather-label-color = Couleur du libellé
    .description = Couleur du texte du libellé

settings-modules-weather-label-max-length = Longueur max. du libellé
    .description = Nombre maximal de caractères avant troncature

settings-modules-weather-button-bg-color = Arrière-plan du bouton
    .description = Couleur d'arrière-plan du bouton

settings-modules-weather-right-click = Clic droit
    .description = Commande à exécuter au clic droit

settings-modules-weather-middle-click = Clic central
    .description = Commande à exécuter au clic central

settings-modules-weather-scroll-up = Défilement vers le haut
    .description = Commande à exécuter au défilement vers le haut

settings-modules-weather-scroll-down = Défilement vers le bas
    .description = Commande à exécuter au défilement vers le bas

settings-modules-weather-time-format = Format de l'heure
    .description = Format d'affichage de l'heure pour les prévisions

settings-modules-weather-left-click = Clic gauche
    .description = Action au clic gauche

settings-modules-weather-refresh-interval-seconds = Intervalle de rafraîchissement
    .description = Fréquence de récupération des données météo (secondes)

## Format placeholders

settings-modules-weather-format-placeholder-temp = Température actuelle

settings-modules-weather-format-placeholder-temp-unit = Symbole de l'unité de température (°F ou °C)

settings-modules-weather-format-placeholder-feels-like = Température ressentie

settings-modules-weather-format-placeholder-condition = Conditions météo (ex. : nuageux)

settings-modules-weather-format-placeholder-humidity = Humidité en pourcentage (ex. : 65%)

settings-modules-weather-format-placeholder-wind-speed = Vitesse du vent avec son unité (ex. : 12 km/h)

settings-modules-weather-format-placeholder-wind-dir = Direction du vent (ex. : NW)

settings-modules-weather-format-placeholder-high = Température maximale du jour (vide sans prévisions)

settings-modules-weather-format-placeholder-low = Température minimale du jour (vide sans prévisions)
