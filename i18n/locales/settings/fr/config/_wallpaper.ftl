### Configuration lys-shell - Paramètres du fond d'écran

## Moteur

settings-wallpaper-engine-enabled = Moteur de fond d'écran
    .description = Activer le moteur de fond d'écran intégré de lys-shell (désactiver pour utiliser hyprpaper, swaybg, etc.)

## Transition

settings-wallpaper-transition-type = Type de transition
    .description = Type d'animation lors du changement de fond d'écran

settings-wallpaper-transition-duration = Durée de transition
    .description = Durée de l'animation en secondes

settings-wallpaper-transition-fps = IPS de transition
    .description = Fréquence d'images de l'animation

## Rotation

settings-wallpaper-cycling-enabled = Rotation activée
    .description = Alterner automatiquement entre les fonds d'écran d'un répertoire

settings-wallpaper-cycling-directory = Répertoire de rotation
    .description = Répertoire contenant les images de fond d'écran

settings-wallpaper-cycling-mode = Mode de rotation
    .description = Ordre de rotation des fonds d'écran (séquentiel ou aléatoire)

settings-wallpaper-cycling-interval-mins = Intervalle de rotation
    .description = Temps entre les changements de fond d'écran en minutes

settings-wallpaper-cycling-same-image = Même image sur tous les moniteurs
    .description = Afficher le même fond d'écran en rotation sur tous les moniteurs (n'affecte que le mode aléatoire)

settings-wallpaper-monitors = Fonds d'écran par moniteur
    .description = Assignation de fond d'écran par moniteur


## Variantes de FitMode
enum-fit-mode-fill = Remplir
enum-fit-mode-fit = Ajuster
enum-fit-mode-center = Centrer
enum-fit-mode-stretch = Étirer

## Variantes de TransitionType
enum-transition-type-none = Aucune
enum-transition-type-simple = Simple
enum-transition-type-fade = Fondu
enum-transition-type-left = Gauche
enum-transition-type-right = Droite
enum-transition-type-top = Haut
enum-transition-type-bottom = Bas
enum-transition-type-wipe = Balayage
enum-transition-type-wave = Vague
enum-transition-type-grow = Croissance
enum-transition-type-center = Centre
enum-transition-type-outer = Extérieur
enum-transition-type-any = Quelconque
enum-transition-type-random = Aléatoire

## Variantes de CyclingMode
enum-cycling-mode-sequential = Séquentiel
enum-cycling-mode-shuffle = Aléatoire

settings-wallpaper-wallpaper = Image du fond d'écran
    .description = Un seul fichier image pour tous les moniteurs (laisser vide pour la rotation ou un fond par moniteur)
settings-wallpaper-fit-mode = Mise à l'échelle
    .description = Manière d'adapter le fond d'écran à l'écran (remplir, ajuster, centrer, étirer)
settings-wallpaper-cycling-options = Options de rotation
    .description = Ordre, intervalle et comportement multi-moniteurs pendant la rotation
