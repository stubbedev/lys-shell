### lys-shell Configuration - Module Bitwarden

## Configuration du module Bitwarden

settings-modules-bitwarden-server = Serveur
    .description = bitwarden.com, bitwarden.eu ou l'URL de base d'un serveur auto-hébergé. Un compte connecté garde le serveur de sa connexion

settings-modules-bitwarden-email = E-mail
    .description = E-mail prérempli dans le formulaire de connexion

settings-modules-bitwarden-lock-with-session = Verrouiller avec la session
    .description = Verrouiller le coffre lorsque la session se verrouille

settings-modules-bitwarden-unlock-with-session = Déverrouiller avec la session
    .description = Déverrouiller le coffre avec le mot de passe de l'écran de verrouillage, s'il est le mot de passe principal

settings-modules-bitwarden-clipboard-clear = Vider le presse-papiers après (secondes)
    .description = Effacer un secret copié après ce nombre de secondes, sauf si autre chose a été copié. 0 le laisse

settings-modules-bitwarden-network-folder = Dossier réseau
    .description = Dossier du coffre dont les identifiants, nommés comme la connexion, répondent aux demandes de mot de passe Wi-Fi et VPN tant que le coffre est ouvert. Vide : aucun

settings-modules-bitwarden-portal-folder = Dossier des secrets du portail
    .description = Dossier du coffre où sont gardées les clés de trousseau des applications isolées, une fois chacune autorisée. Vide : trousseau seulement

settings-modules-bitwarden-generator-passwords = Préréglages de mot de passe
    .description = Mots de passe aléatoires que le générateur du menu copie

settings-modules-bitwarden-generator-passphrases = Préréglages de phrase de passe
    .description = Phrases de passe en mots de la grande liste de l'EFF que le générateur du menu copie

enum-password-charset-lowercase = Minuscules (a-z)
enum-password-charset-uppercase = Majuscules (A-Z)
enum-password-charset-digits = Chiffres (0-9)
enum-password-charset-symbols = Symboles (!@#$%^&*)

settings-modules-bitwarden-format = Format
    .description = Chaîne de format du libellé. Espaces réservés : {"{{ count }}"} (éléments du coffre ouvert)

settings-modules-bitwarden-icon-locked = Icône verrouillé
    .description = Icône lorsque le coffre est verrouillé ou déconnecté

settings-modules-bitwarden-icon-unlocked = Icône déverrouillé
    .description = Icône lorsque le coffre est ouvert

settings-modules-bitwarden-border-show = Afficher la bordure
    .description = Afficher une bordure autour du bouton

settings-modules-bitwarden-border-color = Couleur de la bordure
    .description = Jeton de couleur de la bordure

settings-modules-bitwarden-icon-show = Afficher l'icône
    .description = Afficher l'icône du module

settings-modules-bitwarden-icon-color = Couleur de l'icône
    .description = Couleur de premier plan de l'icône

settings-modules-bitwarden-icon-bg-color = Arrière-plan de l'icône
    .description = Couleur d'arrière-plan du conteneur d'icône

settings-modules-bitwarden-label-show = Afficher le libellé
    .description = Afficher le libellé

settings-modules-bitwarden-label-color = Couleur du libellé
    .description = Couleur du texte du libellé

settings-modules-bitwarden-label-max-length = Longueur max. du libellé
    .description = Nombre max. de caractères avant troncature

settings-modules-bitwarden-button-bg-color = Arrière-plan du bouton
    .description = Couleur d'arrière-plan du bouton

settings-modules-bitwarden-left-click = Clic gauche
    .description = Action au clic gauche. Ouvre par défaut le menu du coffre

settings-modules-bitwarden-right-click = Clic droit
    .description = Commande shell au clic droit

settings-modules-bitwarden-middle-click = Clic central
    .description = Commande shell au clic central

settings-modules-bitwarden-scroll-up = Défilement vers le haut
    .description = Commande shell au défilement vers le haut

settings-modules-bitwarden-scroll-down = Défilement vers le bas
    .description = Commande shell au défilement vers le bas
