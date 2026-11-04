### Configuration lys-shell - Module Espaces de travail Hyprland

## Configuration du module Espaces de travail Hyprland

settings-modules-hyprland-workspaces-min-workspace-count = Espaces de travail minimum
    .description = Nombre minimal de boutons à afficher (0 = uniquement actifs/occupés)

settings-modules-hyprland-workspaces-monitor-specific = Spécifique au moniteur
    .description = Afficher uniquement les espaces de travail de ce moniteur

settings-modules-hyprland-workspaces-show-special = Afficher les spéciaux
    .description = Inclure les espaces de travail éphémères

settings-modules-hyprland-workspaces-urgent-show = Afficher l'urgence
    .description = Animation pulsée sur les espaces de travail avec des fenêtres urgentes

settings-modules-hyprland-workspaces-display-mode = Mode d'affichage
    .description = Ce qui identifie chaque espace de travail (libellé, icône ou rien)

settings-modules-hyprland-workspaces-label-use-name = Utiliser le nom
    .description = Afficher le nom de l'espace de travail au lieu du numéro

settings-modules-hyprland-workspaces-numbering = Numérotation
    .description = Mode d'affichage des numéros d'espaces de travail (absolu ou relatif)

settings-modules-hyprland-workspaces-divider = Séparateur
    .description = Texte entre l'identifiant de l'espace de travail et les icônes d'applications

settings-modules-hyprland-workspaces-app-icons-show = Afficher les icônes d'applications
    .description = Afficher les icônes de fenêtres par espace de travail

settings-modules-hyprland-workspaces-app-icons-dedupe = Dédupliquer les icônes
    .description = Afficher une seule icône par classe de fenêtre

settings-modules-hyprland-workspaces-app-icons-fallback = Icône de repli
    .description = Icône pour les applications non reconnues

settings-modules-hyprland-workspaces-app-icons-empty = Icône vide
    .description = Icône de substitution pour les espaces de travail vides

settings-modules-hyprland-workspaces-icon-gap = Écart entre icônes
    .description = Espacement entre les icônes d'applications (rem)

settings-modules-hyprland-workspaces-workspace-padding = Marge intérieure de l'espace de travail
    .description = Marge pour le contenu plus large que le cercle : icônes d'applications, séparateur ou nom long. Un libellé court n'en prend pas : elle dimensionne alors le cercle

settings-modules-hyprland-workspaces-icon-size = Taille des icônes
    .description = Multiplicateur d'échelle pour les icônes d'espaces de travail (0.25-3.0)

settings-modules-hyprland-workspaces-label-size = Taille des libellés
    .description = Multiplicateur d'échelle pour les libellés d'espaces de travail (0.25-3.0)

settings-modules-hyprland-workspaces-workspace-ignore = Ignorer les espaces de travail
    .description = Motifs glob pour les identifiants d'espaces de travail à masquer

settings-modules-hyprland-workspaces-active-indicator = Indicateur actif
    .description = Style visuel pour l'espace de travail actif

settings-modules-hyprland-workspaces-active-color = Couleur active
    .description = Couleur des icônes et libellés de l'espace de travail actif

settings-modules-hyprland-workspaces-occupied-color = Couleur occupé
    .description = Couleur des icônes et libellés des espaces de travail occupés

settings-modules-hyprland-workspaces-empty-color = Couleur vide
    .description = Couleur des icônes et libellés des espaces de travail vides

settings-modules-hyprland-workspaces-container-bg-color = Arrière-plan du conteneur
    .description = Couleur d'arrière-plan du conteneur d'espaces de travail

settings-modules-hyprland-workspaces-border-show = Afficher la bordure
    .description = Afficher une bordure autour du conteneur d'espaces de travail

settings-modules-hyprland-workspaces-border-color = Couleur de la bordure
    .description = Couleur de la bordure du conteneur d'espaces de travail

settings-modules-hyprland-workspaces-workspace-map = Carte des espaces de travail
    .description = Remplacements d'icônes et de couleurs par espace de travail

settings-modules-hyprland-workspaces-app-icon-map = Carte des icônes d'applications
    .description = Correspondances de classe/titre de fenêtre vers des icônes

settings-modules-hyprland-workspaces-urgent-mode = Mode d'urgence
    .description = Mode de mise en évidence des espaces de travail urgents (par espace de travail ou par application)


## Variantes de DisplayMode
enum-display-mode-label = Libellé
enum-display-mode-icon = Icône
enum-display-mode-none = Aucun

## Variantes de ActiveIndicator
enum-active-indicator-background = Arrière-plan
enum-active-indicator-underline = Soulignement

settings-modules-hyprland-workspaces-highlight-active-on-other-monitor = Surligner l'actif sur un autre moniteur
    .description = Utiliser une couleur différente pour les espaces de travail actifs sur d'autres moniteurs (uniquement si { settings-modules-hyprland-workspaces-monitor-specific } est désactivé)
settings-modules-hyprland-workspaces-active-on-other-monitor-color = Couleur de l'actif sur un autre moniteur
    .description = Couleur des icônes et libellés des espaces de travail actifs sur d'autres moniteurs (uniquement si { settings-modules-hyprland-workspaces-highlight-active-on-other-monitor } est activé)
settings-modules-hyprland-workspaces-left-click = Clic gauche
    .description = Action au clic gauche
settings-modules-hyprland-workspaces-right-click = Clic droit
    .description = Action au clic droit
settings-modules-hyprland-workspaces-middle-click = Clic central
    .description = Action au clic central
settings-modules-hyprland-workspaces-scroll-up = Défilement vers le haut
    .description = Action au défilement vers le haut
settings-modules-hyprland-workspaces-scroll-down = Défilement vers le bas
    .description = Action au défilement vers le bas
