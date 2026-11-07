

settings-osd-enabled = Activé
    .description = Afficher les indicateurs à l'écran pour le volume, la luminosité, etc.

settings-osd-position = Position
    .description = Position de l'OSD à l'écran (ex. : haut-centre, bas-droite)

settings-osd-duration = Durée
    .description = Durée de visibilité de l'OSD avant fermeture automatique (ms)

settings-osd-monitor = Moniteur
    .description = Moniteur affichant l'OSD : « primary » ou un connecteur comme « DP-1 »

settings-osd-margin = Marge
    .description = Distance entre le bord de l'écran et l'OSD

settings-osd-border = Bordure
    .description = Dessiner une bordure autour de l'indicateur OSD


## Variantes de OsdPosition
enum-osd-position-top-left = Haut gauche
enum-osd-position-top = Haut
enum-osd-position-top-right = Haut droite
enum-osd-position-right = Droite
enum-osd-position-bottom-right = Bas droite
enum-osd-position-bottom = Bas
enum-osd-position-bottom-left = Bas gauche
enum-osd-position-left = Gauche

settings-osd-presets = Préréglages de toasts
    .description = Toasts réutilisables à déclencher avec `lys-shell toast --preset <id>`
settings-osd-layer = Couche
    .description = Couche layer-shell sur laquelle l'OSD est placé. Le mode de déchirement rétrograde la superposition en haut.
settings-osd-text-align = Alignement du texte
    .description = Alignement horizontal du contenu des toasts et bascules de l'OSD
enum-osd-text-align-start = Début
enum-osd-text-align-center = Centre
enum-osd-text-align-end = Fin
settings-osd-media = Lecture en cours
    .description = Changements de piste qui affichent l'OSD de lecture : demandés (touches multimédia et `lys-shell media next`), toujours ou jamais
enum-osd-media-requested = Demandés
enum-osd-media-always = Toujours
enum-osd-media-never = Jamais
