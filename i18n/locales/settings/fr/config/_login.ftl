settings-login-background-mode = Arrière-plan
    .description = Manière de dessiner l'arrière-plan de l'écran de connexion : couleur unie, image ou fond d'écran
settings-login-background-image = Image d'arrière-plan
    .description = Fichier image affiché derrière l'écran de connexion lorsque l'arrière-plan vaut « image »
settings-login-background-color = Couleur d'arrière-plan
    .description = Couleur affichée derrière l'écran de connexion lorsque l'arrière-plan vaut « color »
settings-login-blur = Flou
    .description = Rayon du flou gaussien appliqué aux arrière-plans image et fond d'écran (0 = aucun)
settings-login-show-clock = Afficher l'horloge
    .description = Afficher une horloge au-dessus de la demande d'identifiants
settings-login-clock-format = Format de l'heure
    .description = Format strftime de l'heure sur l'écran de connexion (ex. : %H:%M)
settings-login-date-format = Format de la date
    .description = Format strftime de la date sur l'écran de connexion (ex. : %A, %B %-d)
settings-login-max-attempts = Tentatives maximales
    .description = Échecs de mot de passe sur l'écran de verrouillage avant le blocage de la saisie (0 = illimité). L'écran reste verrouillé.
settings-login-show-failed-attempts = Afficher les tentatives échouées
    .description = Afficher le nombre de tentatives échouées sur l'écran de verrouillage
settings-login-lock-enabled = Activé
    .description = Laisser lys-shell verrouiller la session (répond à loginctl lock-session et à `lys-shell lock`)
settings-login-lock-lock-on-start = Verrouiller au démarrage
    .description = Verrouiller la session dès le démarrage du shell, pour les connexions automatiques où l'écran de verrouillage contrôle l'accès
settings-login-lock-grace-period-ms = Délai de grâce
    .description = Période après le verrouillage pendant laquelle l'écran se déverrouille sans mot de passe (ms, 0 = toujours l'exiger)
settings-login-lock-blank-timeout-ms = Délai avant écran noir
    .description = Noircir l'écran de verrouillage après ce temps d'inactivité (ms, 0 = jamais ; visuel uniquement, le DPMS relève du démon d'inactivité)
settings-login-lock-pam-service = Service PAM
    .description = Service PAM qui vérifie le mot de passe (ex. : system-auth, login)
settings-login-lock-show-media = Afficher la lecture en cours
    .description = Afficher le morceau en cours avec lecture/pause et suivant sur l'écran de verrouillage (pochette locale uniquement)
settings-login-lock-show-battery = Afficher la batterie
    .description = Afficher la charge de la batterie sur l'écran de verrouillage ; absent sans batterie
settings-login-lock-fingerprint = Empreinte digitale
    .description = Déverrouiller avec une empreinte enregistrée (fprintd) en plus du mot de passe
settings-login-greeter-show-user-list = Afficher la liste des utilisateurs
    .description = Afficher au-dessus du champ de nom d'utilisateur des avatars cliquables pour les utilisateurs de la machine
settings-login-greeter-show-power-buttons = Afficher les boutons d'alimentation
    .description = Afficher les boutons d'arrêt et de redémarrage en bas de l'écran de connexion
settings-login-greeter-cursor-theme = Thème de curseur
    .description = Thème Xcursor utilisé sur l'écran de connexion (vide = valeur par défaut du système)
settings-login-greeter-cursor-size = Taille du curseur
    .description = Taille logique du curseur, mise à l'échelle pour chaque écran afin qu'il reste net en HiDPI
settings-login-greeter-screen-reader = Lecteur d'écran
    .description = Lecteur d'écran que lancent le menu d'accessibilité de l'écran de connexion et Super+Alt+S ; masqué s'il n'est pas installé
enum-lock-background-color = Couleur
enum-lock-background-image = Image
enum-lock-background-wallpaper = Fond d'écran
