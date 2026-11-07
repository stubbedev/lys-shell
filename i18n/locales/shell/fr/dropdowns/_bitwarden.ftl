### Menu déroulant Bitwarden

dropdown-bitwarden-title = Bitwarden
dropdown-bitwarden-tooltip-logged-out = Bitwarden : connectez-vous pour ouvrir le coffre
dropdown-bitwarden-tooltip-locked = Bitwarden : le coffre est verrouillé
dropdown-bitwarden-tooltip-unlocked = Bitwarden : le coffre est ouvert
dropdown-bitwarden-unavailable = Coffre indisponible
dropdown-bitwarden-server = Serveur : { $server }
dropdown-bitwarden-account = Verrouillé : { $email }
dropdown-bitwarden-email = E-mail
dropdown-bitwarden-email-placeholder = vous@exemple.com
dropdown-bitwarden-master-password = Mot de passe principal
dropdown-bitwarden-client-id = client_id de la clé d'API
dropdown-bitwarden-client-secret = client_secret de la clé d'API
dropdown-bitwarden-required = Obligatoire
dropdown-bitwarden-login = Se connecter
dropdown-bitwarden-login-tooltip = Se connecter à votre compte Bitwarden
dropdown-bitwarden-use-api-key = Se connecter avec une clé d'API
dropdown-bitwarden-use-api-key-tooltip = Se connecter avec votre clé d'API personnelle, sans second facteur
dropdown-bitwarden-use-password = Se connecter avec le mot de passe principal
dropdown-bitwarden-use-password-tooltip = Se connecter avec votre e-mail et votre mot de passe principal
dropdown-bitwarden-two-factor-title = Connexion en deux étapes
dropdown-bitwarden-two-factor-app = Saisissez le code de votre application d'authentification.
dropdown-bitwarden-two-factor-email = Saisissez le code envoyé à { $email }.
dropdown-bitwarden-new-device-title = Nouvel appareil
dropdown-bitwarden-new-device-desc = Bitwarden a envoyé un code de vérification à { $email } pour confirmer cet appareil.
dropdown-bitwarden-code-placeholder = Code
dropdown-bitwarden-send-code = Envoyer le code
dropdown-bitwarden-send-code-tooltip = Envoyer le code de vérification à l'adresse de votre compte
dropdown-bitwarden-back = Retour
dropdown-bitwarden-back-login-tooltip = Retour à la connexion
dropdown-bitwarden-back-vault-tooltip = Retour au coffre
dropdown-bitwarden-verify = Vérifier
dropdown-bitwarden-verify-tooltip = Vérifier le code et terminer la connexion
dropdown-bitwarden-unlock = Déverrouiller
dropdown-bitwarden-unlock-tooltip = Ouvrir le coffre avec votre mot de passe principal
dropdown-bitwarden-sync = Synchroniser
dropdown-bitwarden-lock = Verrouiller
dropdown-bitwarden-logout = Se déconnecter
dropdown-bitwarden-search = Rechercher dans le coffre
dropdown-bitwarden-no-items = Aucun élément
dropdown-bitwarden-more = { $shown } éléments sur { $total } affichés. Recherchez pour affiner.
dropdown-bitwarden-undecryptable = Impossible à déchiffrer
dropdown-bitwarden-copy = Copier
dropdown-bitwarden-copy-reprompt-tooltip = Vérifier le mot de passe principal et copier le champ protégé
dropdown-bitwarden-cancel = Annuler
dropdown-bitwarden-cancel-tooltip = Ne rien copier et revenir au coffre
dropdown-bitwarden-copy-field = Copier : { $field }
dropdown-bitwarden-copied = Copié : { $field }.
dropdown-bitwarden-copied-clears = Copié : { $field }. Le presse-papiers sera vidé dans { $seconds } s.
dropdown-bitwarden-reprompt-title = Confirmation du mot de passe principal
dropdown-bitwarden-reprompt-desc = { $item } est protégé : saisissez votre mot de passe principal pour copier « { $field } ».
dropdown-bitwarden-field-username = nom d'utilisateur
dropdown-bitwarden-field-password = mot de passe
dropdown-bitwarden-field-totp = code de vérification
dropdown-bitwarden-field-card-number = numéro de carte
dropdown-bitwarden-field-card-code = code de sécurité
dropdown-bitwarden-field-email = e-mail
dropdown-bitwarden-field-notes = note
dropdown-bitwarden-field-passphrase = phrase de passe
dropdown-bitwarden-generator = Générer un mot de passe
dropdown-bitwarden-generate = Générer et copier : { $field }
dropdown-bitwarden-generator-password-caption = { $length } caractères : { $charsets }
dropdown-bitwarden-generator-passphrase-caption = { $words } mots
dropdown-bitwarden-generator-empty = Aucun préréglage de générateur
dropdown-bitwarden-generator-empty-hint = Ajoutez-en sous modules.bitwarden.generator-passwords et generator-passphrases.
dropdown-bitwarden-error-wrong-password = Mot de passe principal incorrect.
dropdown-bitwarden-error-credentials = L'e-mail, le mot de passe principal ou le code est incorrect.
dropdown-bitwarden-error-login-required = La session a expiré. Reconnectez-vous.
dropdown-bitwarden-error-unavailable = Le serveur est indisponible. Réessayez plus tard.
dropdown-bitwarden-error-server = Le serveur a refusé : { $message }
dropdown-bitwarden-error-network = Impossible de joindre le serveur.
launcher-bitwarden-locked = Bitwarden est verrouillé. Saisissez votre mot de passe principal et appuyez sur Entrée.
launcher-bitwarden-logged-out = Connectez-vous d'abord à Bitwarden depuis son menu dans la barre.
launcher-bitwarden-hint = Entrée copie le mot de passe ou le premier champ d'un élément ; Maj+Entrée liste ses champs.
dropdown-bitwarden-totp-live = { $code } · { $seconds } s restantes
dropdown-bitwarden-notify-sync-failed = Bitwarden n’a pas pu se synchroniser
dropdown-bitwarden-notify-logged-out = Bitwarden vous a déconnecté
dropdown-bitwarden-notify-logged-out-body = La session a expiré ou a été révoquée. Reconnectez-vous depuis la barre.
dropdown-bitwarden-sends = Bitwarden Send
dropdown-bitwarden-send-name = Nom
dropdown-bitwarden-send-name-placeholder = Ce que vous envoyez
dropdown-bitwarden-send-text = Texte
dropdown-bitwarden-send-text-placeholder = Le texte qu'ouvre le lien
dropdown-bitwarden-send-deletion = Supprimer après
dropdown-bitwarden-send-max-access = Ouvertures maximales (0 pour aucune limite)
dropdown-bitwarden-send-password = Mot de passe
dropdown-bitwarden-send-password-placeholder = Facultatif, demandé avant d'afficher le texte
dropdown-bitwarden-send-create = Créer et copier le lien
dropdown-bitwarden-send-create-tooltip = Créer le Send et copier son lien de partage
dropdown-bitwarden-send-none = Aucun Send
dropdown-bitwarden-send-delete = Supprimer { $name }
dropdown-bitwarden-send-hours = { $hours ->
    [one] 1 heure
   *[other] { $hours } heures
}
dropdown-bitwarden-send-days = { $days ->
    [one] 1 jour
   *[other] { $days } jours
}
dropdown-bitwarden-send-accesses = { $count ->
    [one] ouvert une fois
   *[other] ouvert { $count } fois
}
dropdown-bitwarden-send-accesses-of = ouvert { $count } fois sur { $max }
dropdown-bitwarden-send-deletes = supprimé le { $date }
dropdown-bitwarden-send-protected = mot de passe
dropdown-bitwarden-send-disabled = désactivé
dropdown-bitwarden-field-send-link = lien du Send
